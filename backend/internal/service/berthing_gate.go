package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/dto"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/repository"
	"gorm.io/gorm"
)

// GateBlockedError carries the structured blocker list back to the handler. The
// vessel call keeps its original status and no business audit is written.
type GateBlockedError struct {
	Result dto.BerthingGateResult
}

func (e *GateBlockedError) Error() string {
	return "berthing release gate blocked the transition"
}

// BerthingGateService enforces the 靠泊放行闸门: a vessel call may only advance
// to moored when a cleared-and-effective safety clearance exists at the same
// berth, and the weather window version pinned by that clearance is still in
// the safe state. All checks plus the transition run atomically.
type BerthingGateService interface {
	Evaluate(context.Context, uint, string, string) (dto.BerthingGateResult, error)
	LatestResults(context.Context) ([]dto.BerthingGateResult, error)
	ReleaseWithGate(context.Context, model.VesselCall, dto.TransitionRequest, string, string) (model.VesselCall, error)
}

type berthingGateService struct {
	vessels    repository.VesselCallRepository
	clearances repository.SafetyClearanceRepository
	windows    repository.WeatherWindowRepository
	gates      repository.BerthingGateRepository
}

func NewBerthingGateService(
	vessels repository.VesselCallRepository,
	clearances repository.SafetyClearanceRepository,
	windows repository.WeatherWindowRepository,
	gates repository.BerthingGateRepository,
) BerthingGateService {
	return &berthingGateService{vessels: vessels, clearances: clearances, windows: windows, gates: gates}
}

// gateSnapshot collects the clearance/window chosen by the evaluation.
type gateSnapshot struct {
	passed        bool
	blockers      []dto.GateBlocker
	clearanceCode string
	windowCode    string
	windowVersion uint
	windowStatus  string
}

// evaluate is the pure read-side rule; it never writes anything.
func (s *berthingGateService) evaluate(ctx context.Context, vessel model.VesselCall) gateSnapshot {
	now := time.Now().UTC()
	snap := gateSnapshot{blockers: make([]dto.GateBlocker, 0)}

	clearances, err := s.clearances.ListByFacility(ctx, vessel.Facility)
	if err != nil {
		snap.blockers = append(snap.blockers, dto.GateBlocker{Code: "gate_query_failed", Message: "无法读取同泊位安全许可，请稍后重试"})
		return snap
	}
	if len(clearances) == 0 {
		snap.blockers = append(snap.blockers, dto.GateBlocker{
			Code: "no_clearance", Message: fmt.Sprintf("泊位 %q 下不存在任何安全许可", vessel.Facility),
		})
		return snap
	}

	cleared := make([]model.SafetyClearance, 0)
	for _, clearance := range clearances {
		if clearance.Status == "cleared" {
			cleared = append(cleared, clearance)
		}
	}
	if len(cleared) == 0 {
		snap.blockers = append(snap.blockers, dto.GateBlocker{
			Code: "no_cleared_clearance", Message: "同泊位下不存在已放行(cleared)的安全许可",
		})
		return snap
	}

	effective := make([]model.SafetyClearance, 0)
	for _, clearance := range cleared {
		if !clearance.EffectiveAt.UTC().After(now) {
			effective = append(effective, clearance)
		}
	}
	if len(effective) == 0 {
		snap.blockers = append(snap.blockers, dto.GateBlocker{
			Code: "clearance_not_effective", Message: "已放行的安全许可尚未到生效时间",
		})
		// 仍然记录首个候选许可，便于页面回读核对。
		snap.clearanceCode = cleared[0].Code
		return snap
	}

	// 记录第一份生效许可的窗口上下文，即使窗口不满足也可回读。
	snap.clearanceCode = effective[0].Code
	seenIssue := make(map[string]bool)
	addIssue := func(blocker dto.GateBlocker) {
		if !seenIssue[blocker.Code] {
			seenIssue[blocker.Code] = true
			snap.blockers = append(snap.blockers, blocker)
		}
	}

	for _, clearance := range effective {
		window, err := s.windows.GetByCode(ctx, clearance.RelatedCode)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				addIssue(dto.GateBlocker{
					Code:    "window_missing",
					Message: fmt.Sprintf("许可 %s 固化的风浪窗口 %s 不存在", clearance.Code, clearance.RelatedCode),
				})
				continue
			}
			snap.blockers = append(snap.blockers, dto.GateBlocker{Code: "gate_query_failed", Message: "无法读取风浪窗口，请稍后重试"})
			return snap
		}
		// 记录首个候选的窗口上下文。
		if snap.windowCode == "" {
			snap.windowCode = window.Code
			snap.windowVersion = clearance.WindowVersion
			snap.windowStatus = window.Status
		}
		versionOK := window.Version == clearance.WindowVersion
		safeOK := window.Status == "safe"
		if !versionOK {
			addIssue(dto.GateBlocker{
				Code: "window_version_mismatch",
				Message: fmt.Sprintf("许可 %s 固化窗口 %s v%d，但当前版本为 v%d，版本不一致",
					clearance.Code, window.Code, clearance.WindowVersion, window.Version),
			})
		}
		if !safeOK {
			addIssue(dto.GateBlocker{
				Code:    "window_not_safe",
				Message: fmt.Sprintf("风浪窗口 %s 当前状态为 %s，未处于 safe 安全状态", window.Code, window.Status),
			})
		}
		if versionOK && safeOK {
			snap.passed = true
			snap.clearanceCode = clearance.Code
			snap.windowCode = window.Code
			snap.windowVersion = window.Version
			snap.windowStatus = window.Status
			snap.blockers = make([]dto.GateBlocker, 0)
			return snap
		}
	}
	return snap
}

func (s *berthingGateService) Evaluate(ctx context.Context, vesselID uint, actor, requestID string) (dto.BerthingGateResult, error) {
	vessel, err := s.vessels.Get(ctx, vesselID)
	if err != nil {
		return dto.BerthingGateResult{}, err
	}
	snap := s.evaluate(ctx, vessel)
	result := dto.BerthingGateResult{
		VesselID: vessel.ID, VesselCode: vessel.Code, VesselStatus: vessel.Status, Facility: vessel.Facility,
		TargetStatus: "moored", Passed: snap.passed, Blockers: snap.blockers,
		ClearanceCode: snap.clearanceCode, WindowCode: snap.windowCode,
		WindowVersion: snap.windowVersion, WindowStatus: snap.windowStatus,
		CheckedAt: time.Now().UTC(), Persisted: false, Actor: actor, RequestID: requestID,
	}
	return result, nil
}

// ReleaseWithGate is invoked by the vessel call service when the requested
// target is moored. On failure it persists a gate check record but never
// touches the vessel call, clearance, weather window or business audit logs.
func (s *berthingGateService) ReleaseWithGate(ctx context.Context, vessel model.VesselCall, input dto.TransitionRequest, actor, requestID string) (model.VesselCall, error) {
	now := time.Now().UTC()
	snap := s.evaluate(ctx, vessel)
	if !snap.passed {
		result := s.buildResult(vessel, snap, now, actor, requestID)
		_ = s.persistCheck(ctx, vessel, "moored", false, snap, result.Blockers, actor, requestID, now)
		return model.VesselCall{}, &GateBlockedError{Result: result}
	}

	audit := model.AuditLog{
		RequestID: defaultText(requestID, "untracked"), Actor: defaultText(actor, "system"),
		Action: "transition", EntityType: "VesselCall", EntityID: vessel.ID,
		BeforeState: vessel.Status, AfterState: "moored", Detail: input.Reason,
		WindowVersion: snap.windowVersion, CreatedAt: now,
	}
	check := model.BerthingGateCheck{
		VesselID: vessel.ID, Code: vessel.Code, Facility: vessel.Facility,
		FromState: vessel.Status, ToState: "moored", Passed: true,
		ClearanceCode: snap.clearanceCode, WindowCode: snap.windowCode,
		WindowVersion: snap.windowVersion, WindowStatus: snap.windowStatus,
		Actor: defaultText(actor, "system"), RequestID: defaultText(requestID, "untracked"), CreatedAt: now,
	}
	err := s.gates.ReleaseMoored(ctx, repository.ReleaseMooredInput{
		VesselID: vessel.ID, ExpectedVersion: input.ExpectedVersion, FromStatus: vessel.Status,
		TargetStatus: "moored", Facility: vessel.Facility, NewVersion: input.ExpectedVersion + 1,
		Now: now, Audit: audit, GateCheck: check,
	})
	if err == nil {
		return s.vessels.Get(ctx, vessel.ID)
	}
	if !errors.Is(err, repository.ErrBerthingGateBlocked) {
		return model.VesselCall{}, fmt.Errorf("release moored gate: %w", err)
	}

	// 条件更新零命中：可能是窗口/许可在评估后变化，也可能是并发重复推进。
	current, getErr := s.vessels.Get(ctx, vessel.ID)
	if getErr != nil {
		return model.VesselCall{}, getErr
	}
	if current.Status == "moored" {
		// 已被其它请求放行成功：并发落败方，不重复留痕，不改动任何记录。
		return model.VesselCall{}, &GateBlockedError{Result: dto.BerthingGateResult{
			VesselID: vessel.ID, VesselCode: vessel.Code, VesselStatus: current.Status, Facility: vessel.Facility,
			TargetStatus: "moored", Passed: false, CheckedAt: time.Now().UTC(), Persisted: false,
			Actor: actor, RequestID: requestID,
			Blockers: []dto.GateBlocker{{
				Code:    "already_moored",
				Message: "靠泊任务已被其他请求放行到已系泊，本次重复推进未生效，请刷新后查看",
			}},
		}}
	}

	// 状态未变但条件更新失败：窗口或许可在推进期间发生变化，重新核对并留痕。
	freshSnap := s.evaluate(ctx, current)
	result := s.buildResult(current, freshSnap, time.Now().UTC(), actor, requestID)
	if result.Passed {
		// 条件仍满足但更新零命中，只可能是版本号过期或并发请求抢先完成；
		// 本次未写入任何业务数据，提示刷新后重试。
		result.Passed = false
		result.Blockers = []dto.GateBlocker{{
			Code:    "gate_race",
			Message: "任务版本已过期或已被其他请求处理（窗口或许可在推进期间变化），请刷新后重试",
		}}
	}
	_ = s.persistCheck(ctx, current, "moored", false, freshSnap, result.Blockers, actor, requestID, time.Now().UTC())
	return model.VesselCall{}, &GateBlockedError{Result: result}
}

func (s *berthingGateService) LatestResults(ctx context.Context) ([]dto.BerthingGateResult, error) {
	checks, err := s.gates.LatestByVessel(ctx)
	if err != nil {
		return nil, err
	}
	vesselPage, err := s.vessels.List(ctx, dto.PageQuery{Page: 1, PageSize: 100})
	if err != nil {
		return nil, err
	}
	vessels := make(map[uint]model.VesselCall, len(vesselPage.Items))
	for _, vessel := range vesselPage.Items {
		vessels[vessel.ID] = vessel
	}
	results := make([]dto.BerthingGateResult, 0, len(checks))
	for _, check := range checks {
		blockers := decodeBlockers(check.BlockersJSON)
		status := check.ToState
		if !check.Passed {
			status = check.FromState
		}
		if vessel, ok := vessels[check.VesselID]; ok {
			status = vessel.Status
		}
		results = append(results, dto.BerthingGateResult{
			VesselID: check.VesselID, VesselCode: check.Code, VesselStatus: status,
			Facility: check.Facility, TargetStatus: check.ToState, Passed: check.Passed,
			Blockers: blockers, ClearanceCode: check.ClearanceCode, WindowCode: check.WindowCode,
			WindowVersion: check.WindowVersion, WindowStatus: check.WindowStatus,
			CheckedAt: check.CreatedAt, Persisted: true, Actor: check.Actor, RequestID: check.RequestID,
		})
	}
	return results, nil
}

func (s *berthingGateService) buildResult(vessel model.VesselCall, snap gateSnapshot, now time.Time, actor, requestID string) dto.BerthingGateResult {
	return dto.BerthingGateResult{
		VesselID: vessel.ID, VesselCode: vessel.Code, VesselStatus: vessel.Status, Facility: vessel.Facility,
		TargetStatus: "moored", Passed: snap.passed, Blockers: snap.blockers,
		ClearanceCode: snap.clearanceCode, WindowCode: snap.windowCode,
		WindowVersion: snap.windowVersion, WindowStatus: snap.windowStatus,
		CheckedAt: now, Persisted: false, Actor: actor, RequestID: requestID,
	}
}

func (s *berthingGateService) persistCheck(ctx context.Context, vessel model.VesselCall, target string, passed bool, snap gateSnapshot, blockers []dto.GateBlocker, actor, requestID string, now time.Time) error {
	payload, err := json.Marshal(blockers)
	if err != nil {
		return err
	}
	check := model.BerthingGateCheck{
		VesselID: vessel.ID, Code: vessel.Code, Facility: vessel.Facility,
		FromState: vessel.Status, ToState: target, Passed: passed,
		ClearanceCode: snap.clearanceCode, WindowCode: snap.windowCode,
		WindowVersion: snap.windowVersion, WindowStatus: snap.windowStatus,
		BlockersJSON: string(payload),
		Actor:        defaultText(actor, "system"),
		RequestID:    defaultText(requestID, "untracked"),
		CreatedAt:    now,
	}
	return s.gates.SaveCheck(ctx, &check)
}

func decodeBlockers(raw string) []dto.GateBlocker {
	if raw == "" {
		return []dto.GateBlocker{}
	}
	var blockers []dto.GateBlocker
	if err := json.Unmarshal([]byte(raw), &blockers); err != nil {
		return []dto.GateBlocker{}
	}
	if blockers == nil {
		blockers = []dto.GateBlocker{}
	}
	return blockers
}

func defaultText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
