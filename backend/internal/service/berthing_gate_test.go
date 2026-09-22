package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/config"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/dto"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var fixtureSeq uint64

type gateFixture struct {
	db         *gorm.DB
	vessels    repository.VesselCallRepository
	clearances repository.SafetyClearanceRepository
	windows    repository.WeatherWindowRepository
	gates      repository.BerthingGateRepository
	security   SecurityService
	svc        BerthingGateService
}

func newGateFixture(t *testing.T) gateFixture {
	t.Helper()
	// 每个测试（含 -count 重复执行）使用独立的共享缓存内存库，并限制单连接以串行化写入。
	serial := atomic.AddUint64(&fixtureSeq, 1)
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "_" + strconv.FormatUint(serial, 10) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// 单一连接：SQLite 写入串行化，仍能验证并发重复推进只有一次成功。
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.AuditLog{},
		&model.VesselCall{}, &model.MooringPlan{},
		&model.WeatherWindow{}, &model.SafetyClearance{},
		&model.BerthingGateCheck{},
	); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	vessels := repository.NewVesselCallRepository(db)
	clearances := repository.NewSafetyClearanceRepository(db)
	windows := repository.NewWeatherWindowRepository(db)
	gates := repository.NewBerthingGateRepository(db)
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	svc := NewBerthingGateService(vessels, clearances, windows, gates)
	return gateFixture{db: db, vessels: vessels, clearances: clearances, windows: windows, gates: gates, security: security, svc: svc}
}

func (f gateFixture) createVessel(t *testing.T, code, facility, status string, version uint) model.VesselCall {
	t.Helper()
	vessel := model.VesselCall{
		BaseModel: model.BaseModel{Code: code, Name: "Vessel " + code, Status: status, Version: version},
		Facility:  facility, Owner: "ops", Category: "regular", RiskLevel: "medium",
		EffectiveAt: time.Now().UTC(), Evidence: "checked",
	}
	if err := f.vessels.Create(context.Background(), &vessel); err != nil {
		t.Fatalf("create vessel: %v", err)
	}
	return vessel
}

func (f gateFixture) createWindow(t *testing.T, code, status string, version uint) model.WeatherWindow {
	t.Helper()
	window := model.WeatherWindow{
		BaseModel: model.BaseModel{Code: code, Name: "Window " + code, Status: status, Version: version},
		Facility:  "berth-x", Owner: "ops", Category: "regular", RiskLevel: "low",
		EffectiveAt: time.Now().UTC(),
	}
	if err := f.windows.Create(context.Background(), &window); err != nil {
		t.Fatalf("create window: %v", err)
	}
	return window
}

func (f gateFixture) createClearance(t *testing.T, code, facility, relatedCode, status string, windowVersion uint, effectiveAt time.Time) model.SafetyClearance {
	t.Helper()
	clearance := model.SafetyClearance{
		BaseModel: model.BaseModel{Code: code, Name: "Clearance " + code, Status: status, Version: 1},
		Facility:  facility, Owner: "ops", Category: "regular", RiskLevel: "low",
		EffectiveAt: effectiveAt, RelatedCode: relatedCode, WindowVersion: windowVersion,
	}
	if err := f.clearances.Create(context.Background(), &clearance); err != nil {
		t.Fatalf("create clearance: %v", err)
	}
	return clearance
}

func gateTransition(version uint) dto.TransitionRequest {
	return dto.TransitionRequest{Status: "moored", ExpectedVersion: version, Reason: "berthing gate release attempt"}
}

func expectBlockedWithCode(t *testing.T, err error, code string) dto.BerthingGateResult {
	t.Helper()
	var blocked *GateBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected GateBlockedError, got %v", err)
	}
	for _, blocker := range blocked.Result.Blockers {
		if blocker.Code == code {
			return blocked.Result
		}
	}
	t.Fatalf("expected blocker code %q, got %+v", code, blocked.Result.Blockers)
	return blocked.Result
}

func auditCount(t *testing.T, f gateFixture) int64 {
	t.Helper()
	_, total, err := f.security.ListAudits(context.Background(), 1, 200, "")
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	return total
}

func TestBerthingGateBlocksWhenNoClearance(t *testing.T) {
	f := newGateFixture(t)
	vessel := f.createVessel(t, "VC-NC", "berth-empty", "approach", 1)

	_, err := f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(1), "operator", "req-1")
	result := expectBlockedWithCode(t, err, "no_clearance")
	if result.Passed || result.VesselStatus != "approach" {
		t.Fatalf("unexpected result: %+v", result)
	}
	// 任务状态/版本不变，业务审计为空，闸门留痕一条失败记录。
	got, _ := f.vessels.Get(context.Background(), vessel.ID)
	if got.Status != "approach" || got.Version != 1 {
		t.Fatalf("vessel mutated on failure: %+v", got)
	}
	if auditCount(t, f) != 0 {
		t.Fatal("failure must not write business audit rows")
	}
	latest, err := f.gates.LatestByVessel(context.Background())
	if err != nil || len(latest) != 1 || latest[0].Passed {
		t.Fatalf("expected one failed persisted gate check, got %+v err=%v", latest, err)
	}
}

func TestBerthingGateBlocksPendingAndIneffectiveClearance(t *testing.T) {
	f := newGateFixture(t)
	window := f.createWindow(t, "WW-P", "safe", 1)

	pending := f.createVessel(t, "VC-P", "berth-pending", "approach", 1)
	f.createClearance(t, "SC-P", "berth-pending", window.Code, "pending", 1, time.Now().Add(-time.Hour))
	_, err := f.svc.ReleaseWithGate(context.Background(), pending, gateTransition(1), "operator", "req-p")
	expectBlockedWithCode(t, err, "no_cleared_clearance")

	notYet := f.createVessel(t, "VC-F", "berth-future", "planned", 3)
	f.createClearance(t, "SC-F", "berth-future", window.Code, "cleared", 1, time.Now().Add(time.Hour))
	_, err = f.svc.ReleaseWithGate(context.Background(), notYet, gateTransition(3), "operator", "req-f")
	expectBlockedWithCode(t, err, "clearance_not_effective")
}

func TestBerthingGateBlocksUnsafeAndVersionDrift(t *testing.T) {
	f := newGateFixture(t)

	restrictedWindow := f.createWindow(t, "WW-R", "restricted", 1)
	vesselR := f.createVessel(t, "VC-R", "berth-r", "approach", 1)
	f.createClearance(t, "SC-R", "berth-r", restrictedWindow.Code, "cleared", 1, time.Now().Add(-time.Hour))
	_, err := f.svc.ReleaseWithGate(context.Background(), vesselR, gateTransition(1), "operator", "req-r")
	expectBlockedWithCode(t, err, "window_not_safe")

	driftWindow := f.createWindow(t, "WW-D", "safe", 2)
	vesselD := f.createVessel(t, "VC-D", "berth-d", "approach", 1)
	f.createClearance(t, "SC-D", "berth-d", driftWindow.Code, "cleared", 1, time.Now().Add(-time.Hour))
	_, err = f.svc.ReleaseWithGate(context.Background(), vesselD, gateTransition(1), "operator", "req-d")
	result := expectBlockedWithCode(t, err, "window_version_mismatch")
	if result.WindowVersion != 1 || result.WindowStatus != "safe" {
		t.Fatalf("expected pinned v1 context with safe status, got %+v", result)
	}

	missingVessel := f.createVessel(t, "VC-M", "berth-m", "approach", 1)
	f.createClearance(t, "SC-M", "berth-m", "WW-GONE", "cleared", 1, time.Now().Add(-time.Hour))
	_, err = f.svc.ReleaseWithGate(context.Background(), missingVessel, gateTransition(1), "operator", "req-m")
	expectBlockedWithCode(t, err, "window_missing")
}

func TestBerthingGatePassesWithMatchingSafeWindow(t *testing.T) {
	f := newGateFixture(t)
	window := f.createWindow(t, "WW-OK", "safe", 4)
	vessel := f.createVessel(t, "VC-OK", "berth-ok", "approach", 2)
	f.createClearance(t, "SC-OK", "berth-ok", window.Code, "cleared", 4, time.Now().Add(-2*time.Hour))

	updated, err := f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(2), "operator", "req-ok")
	if err != nil {
		t.Fatalf("expected gate release to succeed, got %v", err)
	}
	if updated.Status != "moored" || updated.Version != 3 {
		t.Fatalf("unexpected vessel after release: %+v", updated)
	}
	logs, total, _ := f.security.ListAudits(context.Background(), 1, 20, "")
	if total != 1 || len(logs) != 1 {
		t.Fatalf("expected exactly one transition audit, got %d", total)
	}
	if logs[0].WindowVersion != 4 || logs[0].BeforeState != "approach" || logs[0].AfterState != "moored" {
		t.Fatalf("audit did not capture gate context: %+v", logs[0])
	}
	latest, err := f.gates.LatestByVessel(context.Background())
	if err != nil || len(latest) != 1 || !latest[0].Passed || latest[0].WindowVersion != 4 {
		t.Fatalf("expected one passed gate check pinned to v4, got %+v err=%v", latest, err)
	}
}

func TestBerthingGateWindowChangeDuringTransitionRejects(t *testing.T) {
	f := newGateFixture(t)
	window := f.createWindow(t, "WW-C", "safe", 1)
	vessel := f.createVessel(t, "VC-C", "berth-c", "approach", 1)
	clearance := f.createClearance(t, "SC-C", "berth-c", window.Code, "cleared", 1, time.Now().Add(-time.Hour))

	// 模拟推进期间窗口被改为 restricted：原子条件语句必须零命中并阻断。
	window.Status = "restricted"
	if err := f.windows.Update(context.Background(), window.ID, 1, &window); err != nil {
		t.Fatalf("flip window: %v", err)
	}
	_, err := f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(1), "operator", "req-c")
	expectBlockedWithCode(t, err, "window_not_safe")

	got, _ := f.vessels.Get(context.Background(), vessel.ID)
	if got.Status != "approach" || got.Version != 1 {
		t.Fatalf("vessel mutated despite window change: %+v", got)
	}
	reloadedClearance, _ := f.clearances.Get(context.Background(), clearance.ID)
	if reloadedClearance.Status != "cleared" || reloadedClearance.Version != 1 {
		t.Fatalf("clearance mutated on failed release: %+v", reloadedClearance)
	}
}

func TestBerthingGateDuplicateTransitionSucceedsOnce(t *testing.T) {
	f := newGateFixture(t)
	f.createWindow(t, "WW-2", "safe", 1)
	vessel := f.createVessel(t, "VC-2", "berth-2", "approach", 1)
	f.createClearance(t, "SC-2", "berth-2", "WW-2", "cleared", 1, time.Now().Add(-time.Hour))

	// 第一个请求使用正确版本，成功。
	first, err := f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(1), "operator", "req-first")
	if err != nil {
		t.Fatalf("first release: %v", err)
	}
	if first.Status != "moored" {
		t.Fatalf("expected moored, got %s", first.Status)
	}

	// 重复推进（携带旧版本）只能得到 already_moored 阻断，不产生第二条审计或成功留痕。
	_, err = f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(1), "operator", "req-dup")
	result := expectBlockedWithCode(t, err, "already_moored")
	if result.VesselStatus != "moored" {
		t.Fatalf("expected current status moored, got %s", result.VesselStatus)
	}
	logs, total, _ := f.security.ListAudits(context.Background(), 1, 20, "")
	if total != 1 || len(logs) != 1 {
		t.Fatalf("duplicate release wrote extra audit rows: %d", total)
	}
	checks, _ := f.gates.LatestByVessel(context.Background())
	if len(checks) != 1 || !checks[0].Passed {
		t.Fatalf("duplicate release must not add gate checks, got %+v", checks)
	}
}

func TestBerthingGateConcurrentReleaseSucceedsOnce(t *testing.T) {
	f := newGateFixture(t)
	if sqlDB, err := f.db.DB(); err == nil {
		// 多连接才存在真实竞争；加 busy_timeout 让落败方收到锁错误而非立即失败。
		sqlDB.SetMaxOpenConns(8)
	}
	if err := f.db.Exec("PRAGMA busy_timeout = 5000;").Error; err != nil {
		t.Fatalf("set busy timeout: %v", err)
	}
	f.createWindow(t, "WW-CC", "safe", 1)
	vessel := f.createVessel(t, "VC-CC", "berth-cc", "approach", 1)
	f.createClearance(t, "SC-CC", "berth-cc", "WW-CC", "cleared", 1, time.Now().Add(-time.Hour))

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	successes, blocks, others := int32(0), int32(0), int32(0)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.svc.ReleaseWithGate(context.Background(), vessel, gateTransition(1), "operator", "req-concurrent")
			switch {
			case err == nil:
				atomic.AddInt32(&successes, 1)
			case errors.As(err, new(*GateBlockedError)):
				atomic.AddInt32(&blocks, 1)
			default:
				atomic.AddInt32(&others, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly one success, got successes=%d blocks=%d others=%d", successes, blocks, others)
	}
	if blocks != workers-1 {
		t.Fatalf("expected %d blocked duplicate attempts, got blocks=%d others=%d", workers-1, blocks, others)
	}
	got, _ := f.vessels.Get(context.Background(), vessel.ID)
	if got.Status != "moored" || got.Version != 2 {
		t.Fatalf("unexpected final vessel state: %+v", got)
	}
	_, total, _ := f.security.ListAudits(context.Background(), 1, 50, "")
	if total != 1 {
		t.Fatalf("expected exactly one audit row, got %d", total)
	}
}

func TestBerthingGateEvaluateIsReadOnly(t *testing.T) {
	f := newGateFixture(t)
	f.createWindow(t, "WW-E", "safe", 1)
	vessel := f.createVessel(t, "VC-E", "berth-e", "approach", 1)
	f.createClearance(t, "SC-E", "berth-e", "WW-E", "cleared", 1, time.Now().Add(-time.Hour))

	result, err := f.svc.Evaluate(context.Background(), vessel.ID, "operator", "req-eval")
	if err != nil || !result.Passed || result.Persisted {
		t.Fatalf("unexpected evaluate result: %+v err=%v", result, err)
	}
	checks, _ := f.gates.LatestByVessel(context.Background())
	if len(checks) != 0 {
		t.Fatalf("live evaluate must not persist gate checks, got %d", len(checks))
	}
	if auditCount(t, f) != 0 {
		t.Fatal("live evaluate must not write audits")
	}
}
