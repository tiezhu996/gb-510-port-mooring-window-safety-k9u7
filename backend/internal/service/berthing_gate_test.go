package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/config"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/dto"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type berthingGateFixture struct {
	db        *gorm.DB
	vessels   repository.VesselCallRepository
	windows   repository.WeatherWindowRepository
	clearance repository.SafetyClearanceRepository
	security  SecurityService
	service   VesselCallService
}

func newBerthingGateFixture(t *testing.T) *berthingGateFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&model.VesselCall{}, &model.WeatherWindow{}, &model.SafetyClearance{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap database: %v", err)
	}
	// A single connection keeps the in-memory database shared and serializes
	// concurrent transactions so exactly-once behavior is deterministic.
	sqlDB.SetMaxOpenConns(1)
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	fixture := &berthingGateFixture{
		db:        db,
		vessels:   repository.NewVesselCallRepository(db),
		windows:   repository.NewWeatherWindowRepository(db),
		clearance: repository.NewSafetyClearanceRepository(db),
		security:  security,
	}
	fixture.service = NewVesselCallService(fixture.vessels, repository.NewBerthingGateRepository(db), security)
	return fixture
}

func (f *berthingGateFixture) addVessel(t *testing.T, code, status, berth string) model.VesselCall {
	t.Helper()
	item := model.VesselCall{
		BaseModel: model.BaseModel{Code: code, Name: "vessel " + code, Status: status, Version: 1},
		Facility:  berth, Owner: "operations", Category: "test", RiskLevel: "medium",
		EffectiveAt: time.Now().UTC(), Evidence: "checked",
	}
	if err := f.vessels.Create(context.Background(), &item); err != nil {
		t.Fatalf("create vessel: %v", err)
	}
	return item
}

func (f *berthingGateFixture) addWindow(t *testing.T, code, status, berth string, version uint) model.WeatherWindow {
	t.Helper()
	item := model.WeatherWindow{
		BaseModel: model.BaseModel{Code: code, Name: "window " + code, Status: status, Version: version},
		Facility:  berth, Owner: "operations", Category: "test", RiskLevel: "medium",
		EffectiveAt: time.Now().UTC(), Evidence: "checked",
	}
	if err := f.windows.Create(context.Background(), &item); err != nil {
		t.Fatalf("create window: %v", err)
	}
	return item
}

func (f *berthingGateFixture) addClearance(t *testing.T, code, status, berth, windowCode string, windowVersion uint, effectiveAt time.Time) model.SafetyClearance {
	t.Helper()
	item := model.SafetyClearance{
		BaseModel: model.BaseModel{Code: code, Name: "clearance " + code, Status: status, Version: 1},
		Facility:  berth, Owner: "operations", Category: "test", RiskLevel: "medium",
		EffectiveAt: effectiveAt, Evidence: "checked", RelatedCode: windowCode, WindowVersion: windowVersion,
	}
	if err := f.clearance.Create(context.Background(), &item); err != nil {
		t.Fatalf("create clearance: %v", err)
	}
	return item
}

func (f *berthingGateFixture) auditTotal(t *testing.T) int64 {
	t.Helper()
	_, total, err := f.security.ListAudits(context.Background(), 1, 50, "")
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	return total
}

func moorRequest(version uint) dto.TransitionRequest {
	return dto.TransitionRequest{Status: "moored", ExpectedVersion: version, Reason: "berthing release requested"}
}

func requireGateBlocked(t *testing.T, err error) *GateBlockedError {
	t.Helper()
	var gateErr *GateBlockedError
	if !errors.As(err, &gateErr) {
		t.Fatalf("expected gate blocked error, got %v", err)
	}
	if len(gateErr.Result.Blockers) == 0 {
		t.Fatalf("gate blocked error must list blocking items: %+v", gateErr.Result)
	}
	return gateErr
}

func TestBerthingGateReleasesWhenClearanceAndWindowAlign(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 1"
	fixture.addWindow(t, "WW-G1", "safe", berth, 2)
	fixture.addClearance(t, "SC-G1", "cleared", berth, "WW-G1", 2, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G1", "approach", berth)

	gate, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("read gate: %v", err)
	}
	if !gate.Required || !gate.Allowed || len(gate.Blockers) != 0 {
		t.Fatalf("expected passable gate, got %+v", gate)
	}
	if gate.ClearanceCode != "SC-G1" || gate.WindowCode != "WW-G1" || gate.WindowVersion != 2 {
		t.Fatalf("gate must identify the clearance and window: %+v", gate)
	}

	released, err := fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-release")
	if err != nil {
		t.Fatalf("release to moored: %v", err)
	}
	if released.Status != "moored" || released.Version != 2 {
		t.Fatalf("unexpected released state: %+v", released)
	}
	logs, total, err := fixture.security.ListAudits(context.Background(), 1, 20, "")
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("expected exactly one audit entry, total=%d", total)
	}
	audit := logs[0]
	if audit.Action != "transition" || audit.BeforeState != "approach" || audit.AfterState != "moored" || audit.WindowVersion != 2 {
		t.Fatalf("audit must capture the release and window version: %+v", audit)
	}
}

func TestBerthingGateBlocksWithoutReleasedClearance(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 2"
	fixture.addWindow(t, "WW-G2", "safe", berth, 1)
	fixture.addClearance(t, "SC-G2", "pending", berth, "WW-G2", 1, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G2", "planned", berth)

	gate, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("read gate: %v", err)
	}
	if gate.Allowed || len(gate.Blockers) == 0 {
		t.Fatalf("expected blocked gate with reasons, got %+v", gate)
	}

	_, err = fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-blocked")
	gateErr := requireGateBlocked(t, err)
	if gateErr.Result.Allowed {
		t.Fatalf("blocked result must not be allowed: %+v", gateErr.Result)
	}
	current, getErr := fixture.vessels.Get(context.Background(), vessel.ID)
	if getErr != nil {
		t.Fatalf("reload vessel: %v", getErr)
	}
	if current.Status != "planned" || current.Version != 1 {
		t.Fatalf("blocked release must keep the original state: %+v", current)
	}
	if total := fixture.auditTotal(t); total != 0 {
		t.Fatalf("blocked release must not write audits, got %d", total)
	}
}

func TestBerthingGateBlocksWhenClearanceNotYetEffective(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 3"
	fixture.addWindow(t, "WW-G3", "safe", berth, 1)
	fixture.addClearance(t, "SC-G3", "cleared", berth, "WW-G3", 1, time.Now().UTC().Add(2*time.Hour))
	vessel := fixture.addVessel(t, "VC-G3", "approach", berth)

	_, err := fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-effective")
	gateErr := requireGateBlocked(t, err)
	found := false
	for _, check := range gateErr.Result.Checks {
		if check.Code == gateCheckClearanceEffective && !check.Passed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the effectiveness check to fail: %+v", gateErr.Result.Checks)
	}
}

func requireFailedCheck(t *testing.T, result dto.BerthingGateResult, code string) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Code == code {
			if check.Passed {
				t.Fatalf("expected check %s to fail: %+v", code, result.Checks)
			}
			return
		}
	}
	t.Fatalf("expected check %s to be reported: %+v", code, result.Checks)
}

func TestBerthingGateBlocksWhenWindowUnsafeOrVersionDrifts(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berthUnsafe := "Berth Gate 4"
	fixture.addWindow(t, "WW-G4", "restricted", berthUnsafe, 1)
	fixture.addClearance(t, "SC-G4", "cleared", berthUnsafe, "WW-G4", 1, time.Now().UTC().Add(-time.Hour))
	unsafeVessel := fixture.addVessel(t, "VC-G4", "approach", berthUnsafe)

	_, err := fixture.service.Transition(context.Background(), unsafeVessel.ID, moorRequest(1), "operator", "req-unsafe")
	gateErr := requireGateBlocked(t, err)
	requireFailedCheck(t, gateErr.Result, gateCheckWindowSafe)

	berthDrift := "Berth Gate 5"
	fixture.addWindow(t, "WW-G5", "safe", berthDrift, 3)
	fixture.addClearance(t, "SC-G5", "cleared", berthDrift, "WW-G5", 2, time.Now().UTC().Add(-time.Hour))
	driftVessel := fixture.addVessel(t, "VC-G5", "approach", berthDrift)

	_, err = fixture.service.Transition(context.Background(), driftVessel.ID, moorRequest(1), "operator", "req-drift")
	gateErr = requireGateBlocked(t, err)
	requireFailedCheck(t, gateErr.Result, gateCheckWindowVersion)
}

func TestBerthingGateBlocksWhenWindowChangesDuringRelease(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 6"
	window := fixture.addWindow(t, "WW-G6", "safe", berth, 1)
	fixture.addClearance(t, "SC-G6", "cleared", berth, "WW-G6", 1, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G6", "approach", berth)

	gate, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil || !gate.Allowed {
		t.Fatalf("gate should allow the release before the window changes: %+v %v", gate, err)
	}

	// The window turns restricted while the operator confirms the release.
	window.Status = "restricted"
	window.Version = 2
	window.UpdatedAt = time.Now().UTC()
	if err := fixture.windows.Update(context.Background(), window.ID, 1, &window); err != nil {
		t.Fatalf("update window: %v", err)
	}

	_, err = fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-changed")
	requireGateBlocked(t, err)
	current, getErr := fixture.vessels.Get(context.Background(), vessel.ID)
	if getErr != nil {
		t.Fatalf("reload vessel: %v", getErr)
	}
	if current.Status != "approach" {
		t.Fatalf("window change during release must not release the vessel: %+v", current)
	}
	if total := fixture.auditTotal(t); total != 0 {
		t.Fatalf("blocked release must not write audits, got %d", total)
	}
}

func TestBerthingGateConcurrentReleaseSucceedsOnce(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 7"
	fixture.addWindow(t, "WW-G7", "safe", berth, 1)
	fixture.addClearance(t, "SC-G7", "cleared", berth, "WW-G7", 1, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G7", "approach", berth)

	const attempts = 8
	var wait sync.WaitGroup
	successes := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-concurrent")
			successes <- err
		}()
	}
	wait.Wait()
	close(successes)

	succeeded := 0
	for err := range successes {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent release must succeed exactly once, got %d", succeeded)
	}
	current, err := fixture.vessels.Get(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("reload vessel: %v", err)
	}
	if current.Status != "moored" || current.Version != 2 {
		t.Fatalf("unexpected final vessel state: %+v", current)
	}
	if total := fixture.auditTotal(t); total != 1 {
		t.Fatalf("exactly one release audit must exist, got %d", total)
	}

	// A repeated advance after the successful one is rejected as well.
	_, err = fixture.service.Transition(context.Background(), vessel.ID, moorRequest(2), "operator", "req-repeat")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeated release must be rejected, got %v", err)
	}
}

func TestBerthingGateFailureLeavesEveryRecordUntouched(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 8"
	window := fixture.addWindow(t, "WW-G8", "restricted", berth, 4)
	clearance := fixture.addClearance(t, "SC-G8", "cleared", berth, "WW-G8", 4, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G8", "approach", berth)

	_, err := fixture.service.Transition(context.Background(), vessel.ID, moorRequest(1), "operator", "req-untouched")
	requireGateBlocked(t, err)

	currentVessel, err := fixture.vessels.Get(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("reload vessel: %v", err)
	}
	if currentVessel.Status != vessel.Status || currentVessel.Version != vessel.Version || !currentVessel.UpdatedAt.Equal(vessel.UpdatedAt) {
		t.Fatalf("failed release must not touch the task: %+v", currentVessel)
	}
	currentWindow, err := fixture.windows.Get(context.Background(), window.ID)
	if err != nil {
		t.Fatalf("reload window: %v", err)
	}
	if currentWindow.Status != window.Status || currentWindow.Version != window.Version {
		t.Fatalf("failed release must not touch the window: %+v", currentWindow)
	}
	currentClearance, err := fixture.clearance.Get(context.Background(), clearance.ID)
	if err != nil {
		t.Fatalf("reload clearance: %v", err)
	}
	if currentClearance.Status != clearance.Status || currentClearance.Version != clearance.Version {
		t.Fatalf("failed release must not touch the clearance: %+v", currentClearance)
	}
	if total := fixture.auditTotal(t); total != 0 {
		t.Fatalf("failed release must not touch the audit trail, got %d entries", total)
	}
}

func TestVesselCallUngatedTransitionsStayUnchanged(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 9"
	vessel := fixture.addVessel(t, "VC-G9", "planned", berth)

	// No clearance or window exists on this berth; non-moored transitions
	// must not consult the gate at all.
	advanced, err := fixture.service.Transition(context.Background(), vessel.ID, dto.TransitionRequest{
		Status: "approach", ExpectedVersion: 1, Reason: "vessel inbound",
	}, "operator", "req-approach")
	if err != nil {
		t.Fatalf("planned -> approach must stay ungated: %v", err)
	}
	if advanced.Status != "approach" {
		t.Fatalf("unexpected state after approach: %+v", advanced)
	}
}

func TestVesselCallDepartureStaysUngated(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 10"
	vessel := fixture.addVessel(t, "VC-G10", "moored", berth)

	departed, err := fixture.service.Transition(context.Background(), vessel.ID, dto.TransitionRequest{
		Status: "departed", ExpectedVersion: 1, Reason: "vessel outbound",
	}, "operator", "req-depart")
	if err != nil {
		t.Fatalf("moored -> departed must stay ungated: %v", err)
	}
	if departed.Status != "departed" {
		t.Fatalf("unexpected state after departure: %+v", departed)
	}

	gate, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("read gate for departed vessel: %v", err)
	}
	if gate.Required {
		t.Fatalf("departed vessels must not require the gate: %+v", gate)
	}
}

func TestBerthingGateReadbackIsRepeatable(t *testing.T) {
	fixture := newBerthingGateFixture(t)
	berth := "Berth Gate 11"
	fixture.addClearance(t, "SC-G11", "pending", berth, "WW-G11", 1, time.Now().UTC().Add(-time.Hour))
	vessel := fixture.addVessel(t, "VC-G11", "approach", berth)

	first, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("first gate read: %v", err)
	}
	second, err := fixture.service.BerthingGate(context.Background(), vessel.ID)
	if err != nil {
		t.Fatalf("second gate read: %v", err)
	}
	if first.Allowed || second.Allowed {
		t.Fatalf("gate must stay blocked without a released clearance: %+v", first)
	}
	if len(first.Blockers) != len(second.Blockers) || first.Blockers[0] != second.Blockers[0] {
		t.Fatalf("gate readback must be repeatable after a refresh: %+v vs %+v", first.Blockers, second.Blockers)
	}
}
