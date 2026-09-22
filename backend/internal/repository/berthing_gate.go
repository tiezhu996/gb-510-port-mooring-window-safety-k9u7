package repository

import (
	"context"
	"strings"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/constants"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BerthingGateSnapshot carries every record the berthing release gate needs so
// the service can evaluate the rules without issuing further queries.
type BerthingGateSnapshot struct {
	Vessel     model.VesselCall
	Clearances []model.SafetyClearance
	Windows    map[string]model.WeatherWindow
}

// BerthingGateRepository owns the atomic read-check-write behind the berthing
// release gate. The gate spans 船舶靠泊, 安全许可 and 风浪窗口, so it lives in
// its own repository instead of leaking cross-aggregate queries into the
// single-entity stores.
type BerthingGateRepository interface {
	// LoadSnapshot reads the vessel, the released clearances on the same berth
	// and their referenced weather windows without locking. It backs the
	// read-only gate endpoint.
	LoadSnapshot(ctx context.Context, vesselID uint) (BerthingGateSnapshot, error)
	// ApplyRelease runs the whole release inside one transaction: the snapshot
	// is re-read with row locks (where the dialect supports them), decide
	// evaluates the gate, the vessel update is guarded by the expected version
	// and the audit row is inserted in the same commit. Any error rolls every
	// write back, so a failed attempt never touches task, clearance, window or
	// audit records.
	ApplyRelease(ctx context.Context, vesselID uint, expectedVersion uint, decide func(BerthingGateSnapshot) (*model.AuditLog, error)) (model.VesselCall, error)
}

type berthingGateRepository struct{ db *gorm.DB }

func NewBerthingGateRepository(db *gorm.DB) BerthingGateRepository {
	return &berthingGateRepository{db: db}
}

func (r *berthingGateRepository) LoadSnapshot(ctx context.Context, vesselID uint) (BerthingGateSnapshot, error) {
	return r.loadSnapshot(r.db.WithContext(ctx), vesselID, false)
}

func (r *berthingGateRepository) ApplyRelease(ctx context.Context, vesselID uint, expectedVersion uint, decide func(BerthingGateSnapshot) (*model.AuditLog, error)) (model.VesselCall, error) {
	var released model.VesselCall
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot, err := r.loadSnapshot(tx, vesselID, true)
		if err != nil {
			return err
		}
		audit, err := decide(snapshot)
		if err != nil {
			return err
		}
		result := tx.Model(&model.VesselCall{}).
			Where("id = ? AND version = ?", vesselID, expectedVersion).
			Updates(map[string]any{
				"status":     string(constants.CallStateMoored),
				"version":    expectedVersion + 1,
				"updated_at": time.Now().UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if audit != nil {
			if err := tx.Create(audit).Error; err != nil {
				return err
			}
		}
		return tx.First(&released, vesselID).Error
	})
	return released, err
}

func (r *berthingGateRepository) loadSnapshot(tx *gorm.DB, vesselID uint, lock bool) (BerthingGateSnapshot, error) {
	var snapshot BerthingGateSnapshot
	vesselQuery := tx
	if lock {
		vesselQuery = lockForUpdate(vesselQuery)
	}
	if err := vesselQuery.First(&snapshot.Vessel, vesselID).Error; err != nil {
		return BerthingGateSnapshot{}, err
	}
	clearances := make([]model.SafetyClearance, 0)
	clearanceQuery := tx.
		Where("facility = ? AND status = ?", snapshot.Vessel.Facility, string(constants.ClearanceStateCleared)).
		Order("updated_at DESC, id DESC")
	if lock {
		clearanceQuery = lockForUpdate(clearanceQuery)
	}
	if err := clearanceQuery.Find(&clearances).Error; err != nil {
		return BerthingGateSnapshot{}, err
	}
	snapshot.Clearances = clearances
	codes := make([]string, 0, len(clearances))
	seen := make(map[string]bool, len(clearances))
	for _, clearance := range clearances {
		code := strings.TrimSpace(clearance.RelatedCode)
		if code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	snapshot.Windows = make(map[string]model.WeatherWindow, len(codes))
	if len(codes) > 0 {
		windows := make([]model.WeatherWindow, 0, len(codes))
		windowQuery := tx.Where("code IN ?", codes)
		if lock {
			windowQuery = lockForUpdate(windowQuery)
		}
		if err := windowQuery.Find(&windows).Error; err != nil {
			return BerthingGateSnapshot{}, err
		}
		for _, window := range windows {
			snapshot.Windows[window.Code] = window
		}
	}
	return snapshot, nil
}

// lockForUpdate applies a row lock on dialects that support it. SQLite (used
// for local development and tests) serializes writers on its own, and its
// read inside a transaction holds a shared lock until commit, so the gate
// stays atomic there without the clause.
func lockForUpdate(db *gorm.DB) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return db
	}
}
