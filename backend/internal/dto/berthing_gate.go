package dto

import "time"

// GateBlocker 描述靠泊放行闸门的单个阻断项。Code 供前端稳定分支判断，
// Message 为可直接展示的中文原因。
type GateBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BerthingGateResult 是靠泊放行闸门的核对结果契约，供实时预检接口和
// 闸门留痕（BerthingGateCheck）回读共用。
type BerthingGateResult struct {
	VesselID      uint          `json:"vesselId"`
	VesselCode    string        `json:"vesselCode"`
	VesselStatus  string        `json:"vesselStatus"`
	Facility      string        `json:"facility"`
	TargetStatus  string        `json:"targetStatus"`
	Passed        bool          `json:"passed"`
	Blockers      []GateBlocker `json:"blockers"`
	ClearanceCode string        `json:"clearanceCode,omitempty"`
	WindowCode    string        `json:"windowCode,omitempty"`
	WindowVersion uint          `json:"windowVersion,omitempty"`
	WindowStatus  string        `json:"windowStatus,omitempty"`
	CheckedAt     time.Time     `json:"checkedAt"`
	// Persisted 表示该结果是否已落库（实时预检为 false，最近一次留痕为 true）。
	Persisted bool   `json:"persisted"`
	Actor     string `json:"actor,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}
