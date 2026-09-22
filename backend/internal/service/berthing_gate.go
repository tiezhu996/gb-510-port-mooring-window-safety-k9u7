package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/constants"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/dto"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/repository"
)

// Gate check codes are stable identifiers shared with the frontend so the
// vessel page can render the reason list without parsing free text.
const (
	gateCheckNotRequired        = "gate_not_required"
	gateCheckClearanceReleased  = "clearance_released"
	gateCheckClearanceEffective = "clearance_effective"
	gateCheckWindowLinked       = "window_linked"
	gateCheckWindowSafe         = "window_safe"
	gateCheckWindowVersion      = "window_version_match"
)

// GateBlockedError carries the full gate evaluation so the API can return the
// blocking items while the transaction guarantees nothing was written.
type GateBlockedError struct {
	Result dto.BerthingGateResult
}

func (e *GateBlockedError) Error() string {
	return "靠泊放行闸门未通过: " + strings.Join(e.Result.Blockers, "; ")
}

// BerthingGate evaluates the release gate for a vessel without writing
// anything. The vessel page calls it to display the current check result and
// can reload it after a refresh.
func (s *vesselCallService) BerthingGate(ctx context.Context, id uint) (dto.BerthingGateResult, error) {
	snapshot, err := s.gate.LoadSnapshot(ctx, id)
	if err != nil {
		return dto.BerthingGateResult{}, err
	}
	return evaluateBerthingGate(snapshot, time.Now().UTC()), nil
}

// releaseToMoored advances a vessel from planned/approach to moored. The gate
// evaluation, the vessel update and the audit insert commit atomically; when
// the gate blocks, the error carries the blocking items and no record changes.
func (s *vesselCallService) releaseToMoored(ctx context.Context, current model.VesselCall, input dto.TransitionRequest, actor, requestID string) (model.VesselCall, error) {
	if actor == "" {
		actor = "system"
	}
	if requestID == "" {
		requestID = "untracked"
	}
	decide := func(snapshot repository.BerthingGateSnapshot) (*model.AuditLog, error) {
		if snapshot.Vessel.Version != input.ExpectedVersion || snapshot.Vessel.Status != current.Status {
			return nil, repository.ErrVersionConflict
		}
		result := evaluateBerthingGate(snapshot, time.Now().UTC())
		if !result.Allowed {
			return nil, &GateBlockedError{Result: result}
		}
		return &model.AuditLog{
			Actor: actor, RequestID: requestID, Action: "transition", EntityType: "VesselCall",
			EntityID: current.ID, BeforeState: current.Status, AfterState: string(constants.CallStateMoored),
			Detail: strings.TrimSpace(input.Reason), WindowVersion: result.WindowVersion, CreatedAt: time.Now().UTC(),
		}, nil
	}
	return s.gate.ApplyRelease(ctx, current.ID, input.ExpectedVersion, decide)
}

// evaluateBerthingGate is the pure rule set behind the release gate. It runs
// both for the read-only endpoint and inside the release transaction, so the
// displayed result always matches the enforced decision.
func evaluateBerthingGate(snapshot repository.BerthingGateSnapshot, now time.Time) dto.BerthingGateResult {
	vessel := snapshot.Vessel
	result := dto.BerthingGateResult{
		VesselID: vessel.ID, VesselCode: vessel.Code, Berth: vessel.Facility,
		FromStatus: vessel.Status, TargetStatus: string(constants.CallStateMoored),
		Checks: make([]dto.BerthingGateCheck, 0, 5), Blockers: make([]string, 0),
		EvaluatedAt: now,
	}
	result.Required = vessel.Status == string(constants.CallStatePlanned) || vessel.Status == string(constants.CallStateApproach)
	if !result.Required {
		result.Allowed = true
		result.Checks = append(result.Checks, gateCheck(gateCheckNotRequired, "放行闸门", true,
			fmt.Sprintf("当前状态 %s 无需靠泊放行闸门", vessel.Status)))
		return result
	}

	if len(snapshot.Clearances) == 0 {
		result.Checks = append(result.Checks, gateCheck(gateCheckClearanceReleased, "许可已放行", false,
			fmt.Sprintf("泊位 %s 缺少已放行的安全许可", vessel.Facility)))
		return finishBerthingGate(result)
	}
	result.Checks = append(result.Checks, gateCheck(gateCheckClearanceReleased, "许可已放行", true,
		fmt.Sprintf("泊位 %s 存在 %d 条已放行的安全许可", vessel.Facility, len(snapshot.Clearances))))

	candidates := make([]model.SafetyClearance, 0, len(snapshot.Clearances))
	for _, clearance := range snapshot.Clearances {
		if !clearance.EffectiveAt.After(now) {
			candidates = append(candidates, clearance)
		}
	}
	if len(candidates) == 0 {
		result.Checks = append(result.Checks, gateCheck(gateCheckClearanceEffective, "许可已生效", false,
			"已放行的安全许可尚未到生效时间"))
		return finishBerthingGate(result)
	}
	result.Checks = append(result.Checks, gateCheck(gateCheckClearanceEffective, "许可已生效", true,
		fmt.Sprintf("许可 %s 已生效", candidates[0].Code)))

	// Candidates are ordered latest first. The gate releases as soon as one
	// clearance lines up with a safe, version-consistent window; otherwise the
	// most recent candidate explains the blockage.
	var failed []dto.BerthingGateCheck
	for _, candidate := range candidates {
		checks, window := evaluateGateCandidate(candidate, snapshot.Windows)
		if checksPass(checks) {
			result.ClearanceCode = candidate.Code
			result.WindowCode = window.Code
			result.WindowVersion = window.Version
			result.Checks = append(result.Checks, checks...)
			result.Allowed = true
			return result
		}
		if failed == nil {
			failed = checks
			result.ClearanceCode = candidate.Code
		}
	}
	result.Checks = append(result.Checks, failed...)
	return finishBerthingGate(result)
}

func evaluateGateCandidate(clearance model.SafetyClearance, windows map[string]model.WeatherWindow) ([]dto.BerthingGateCheck, model.WeatherWindow) {
	windowCode := strings.TrimSpace(clearance.RelatedCode)
	window, linked := windows[windowCode]
	checks := make([]dto.BerthingGateCheck, 0, 3)
	if windowCode == "" || !linked {
		checks = append(checks, gateCheck(gateCheckWindowLinked, "窗口关联", false,
			fmt.Sprintf("许可 %s 未关联有效的风浪窗口", clearance.Code)))
		return checks, model.WeatherWindow{}
	}
	checks = append(checks, gateCheck(gateCheckWindowLinked, "窗口关联", true,
		fmt.Sprintf("许可 %s 关联风浪窗口 %s", clearance.Code, window.Code)))
	if window.Status != "safe" {
		checks = append(checks, gateCheck(gateCheckWindowSafe, "窗口安全", false,
			fmt.Sprintf("风浪窗口 %s 当前状态为 %s，不是 safe", window.Code, window.Status)))
	} else {
		checks = append(checks, gateCheck(gateCheckWindowSafe, "窗口安全", true,
			fmt.Sprintf("风浪窗口 %s 处于安全状态", window.Code)))
	}
	if window.Version != clearance.WindowVersion {
		checks = append(checks, gateCheck(gateCheckWindowVersion, "版本一致", false,
			fmt.Sprintf("许可 %s 锁定的窗口版本 v%d 与当前版本 v%d 不一致", clearance.Code, clearance.WindowVersion, window.Version)))
	} else {
		checks = append(checks, gateCheck(gateCheckWindowVersion, "版本一致", true,
			fmt.Sprintf("窗口版本 v%d 与许可一致", window.Version)))
	}
	return checks, window
}

func finishBerthingGate(result dto.BerthingGateResult) dto.BerthingGateResult {
	for _, check := range result.Checks {
		if !check.Passed {
			result.Blockers = append(result.Blockers, check.Message)
		}
	}
	result.Allowed = len(result.Blockers) == 0
	return result
}

func checksPass(checks []dto.BerthingGateCheck) bool {
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return len(checks) > 0
}

func gateCheck(code, label string, passed bool, message string) dto.BerthingGateCheck {
	return dto.BerthingGateCheck{Code: code, Label: label, Passed: passed, Message: message}
}
