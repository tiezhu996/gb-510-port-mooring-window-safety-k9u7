package repository

import (
	"context"
	"errors"
	"time"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/model"
	"gorm.io/gorm"
)

// ErrBerthingGateBlocked 表示靠泊放行闸门的原子放行语句未命中任何行：
// 可能是阻断条件仍存在，也可能是并发/版本冲突，调用方需先重新核对再决定如何返回。
var ErrBerthingGateBlocked = errors.New("berthing release gate blocked the transition")

// ReleaseMooredInput 是“条件放行”的全部输入。
type ReleaseMooredInput struct {
	VesselID        uint
	ExpectedVersion uint
	FromStatus      string
	TargetStatus    string
	Facility        string
	NewVersion      uint
	Now             time.Time
	Audit           model.AuditLog
	GateCheck       model.BerthingGateCheck
}

// BerthingGateRepository owns persistence for the 靠泊放行闸门. The release
// operation performs the gate evaluation and the state transition inside one
// atomic conditional statement, so a weather window / clearance change during
// the request (or any concurrent duplicate transition) makes the whole
// operation affect zero rows.
type BerthingGateRepository interface {
	SaveCheck(context.Context, *model.BerthingGateCheck) error
	LatestByVessel(context.Context) ([]model.BerthingGateCheck, error)
	ReleaseMoored(context.Context, ReleaseMooredInput) error
}

type berthingGateRepository struct {
	db *gorm.DB
}

func NewBerthingGateRepository(db *gorm.DB) BerthingGateRepository {
	return &berthingGateRepository{db: db}
}

func (r *berthingGateRepository) SaveCheck(ctx context.Context, check *model.BerthingGateCheck) error {
	return r.db.WithContext(ctx).Create(check).Error
}

// LatestByVessel returns the most recent persisted gate check for every vessel
// call (one row per vessel).
func (r *berthingGateRepository) LatestByVessel(ctx context.Context) ([]model.BerthingGateCheck, error) {
	items := make([]model.BerthingGateCheck, 0)
	err := r.db.WithContext(ctx).Raw(`
SELECT bc.* FROM berthing_gate_checks bc
INNER JOIN (
	SELECT vessel_id, MAX(id) AS max_id FROM berthing_gate_checks GROUP BY vessel_id
) latest ON latest.max_id = bc.id
ORDER BY bc.id DESC`).Scan(&items).Error
	return items, err
}

// ReleaseMoored runs the gated transition in a single transaction:
//   - the vessel_calls UPDATE carries the optimistic-lock version predicate and
//     an EXISTS predicate proving a cleared, effective safety clearance at the
//     same facility whose pinned weather window is still safe at the pinned
//     version; RowsAffected == 0 blocks the release;
//   - the audit row and the passing gate check are written in the same
//     transaction, so success is all-or-nothing and the window cannot change in
//     between the checks and the transition.
func (r *berthingGateRepository) ReleaseMoored(ctx context.Context, input ReleaseMooredInput) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.VesselCall{}).
			Where("id = ? AND version = ? AND status = ?", input.VesselID, input.ExpectedVersion, input.FromStatus).
			Where(`EXISTS (
				SELECT 1 FROM safety_clearances sc
				WHERE sc.deleted_at IS NULL
				  AND sc.facility = ?
				  AND sc.status = ?
				  AND sc.effective_at <= ?
				  AND EXISTS (
					SELECT 1 FROM weather_windows ww
					WHERE ww.deleted_at IS NULL
					  AND ww.code = sc.related_code
					  AND ww.version = sc.window_version
					  AND ww.status = ?
				  )
			)`, input.Facility, "cleared", input.Now, "safe").
			Updates(map[string]any{
				"status":     input.TargetStatus,
				"version":    input.NewVersion,
				"updated_at": input.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrBerthingGateBlocked
		}
		if err := tx.Create(&input.Audit).Error; err != nil {
			return err
		}
		if err := tx.Create(&input.GateCheck).Error; err != nil {
			return err
		}
		return nil
	})
}
