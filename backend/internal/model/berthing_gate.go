package model

import "time"

// BerthingGateCheck 记录每次靠泊放行闸门的核对结果。闸门是船舶推进到
// “已系泊(moored)”前的强制关卡：必须存在同一泊位下已放行且已生效的安全许可，
// 且其固化的风浪窗口版本当前仍处于 safe 状态。
//
// 该表是闸门专用的只读审计留痕（不参与软删除），成功与失败都会落库，
// 供靠泊页刷新后回读；失败记录独立写入，绝不改动任务、许可、窗口和业务审计。
type BerthingGateCheck struct {
	ID        uint   `json:"id" gorm:"primaryKey"`
	VesselID  uint   `json:"vesselId" gorm:"index;not null"`
	Code      string `json:"code" gorm:"size:64;index;not null"`
	Facility  string `json:"facility" gorm:"size:120;index;not null"`
	FromState string `json:"fromState" gorm:"size:40;not null"`
	ToState   string `json:"toState" gorm:"size:40;not null"`
	// Passed 表示本次核对是否允许放行。
	Passed bool `json:"passed" gorm:"index;not null;default:false"`
	// ClearanceCode/WindowCode/WindowVersion 固化本次核对所依据的许可与窗口，
	// 失败时允许为空（例如不存在任何同泊位许可）。
	ClearanceCode string    `json:"clearanceCode" gorm:"size:64"`
	WindowCode    string    `json:"windowCode" gorm:"size:64"`
	WindowVersion uint      `json:"windowVersion" gorm:"not null;default:0"`
	WindowStatus  string    `json:"windowStatus" gorm:"size:40"`
	BlockersJSON  string    `json:"blockers" gorm:"type:text"`
	Actor         string    `json:"actor" gorm:"size:80;index;not null"`
	RequestID     string    `json:"requestId" gorm:"size:64;index;not null"`
	CreatedAt     time.Time `json:"createdAt" gorm:"index"`
}

func (item BerthingGateCheck) TableName() string { return "berthing_gate_checks" }
