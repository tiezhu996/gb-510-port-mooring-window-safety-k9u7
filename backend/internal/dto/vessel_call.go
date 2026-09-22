package dto

import "time"

// BerthingGateCheck is one rule evaluated by the berthing release gate. The
// frontend renders every check so operators can see why a release is blocked.
type BerthingGateCheck struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// BerthingGateResult is the read model returned by the berthing release gate.
// It is recomputed from live data on every request, so the vessel page can
// reload it at any time and after a browser refresh.
type BerthingGateResult struct {
	VesselID      uint                `json:"vesselId"`
	VesselCode    string              `json:"vesselCode"`
	Berth         string              `json:"berth"`
	FromStatus    string              `json:"fromStatus"`
	TargetStatus  string              `json:"targetStatus"`
	Required      bool                `json:"required"`
	Allowed       bool                `json:"allowed"`
	ClearanceCode string              `json:"clearanceCode,omitempty"`
	WindowCode    string              `json:"windowCode,omitempty"`
	WindowVersion uint                `json:"windowVersion,omitempty"`
	Checks        []BerthingGateCheck `json:"checks"`
	Blockers      []string            `json:"blockers"`
	EvaluatedAt   time.Time           `json:"evaluatedAt"`
}

// CreateVesselCall is the public write contract for 船舶靠泊. Status is deliberately
// omitted so callers cannot bypass the service state machine.
type CreateVesselCall struct {
	Code        string    `json:"code" binding:"required,min=2,max=64"`
	Name        string    `json:"name" binding:"required,min=2,max=160"`
	Description string    `json:"description" binding:"max=1000"`
	Facility    string    `json:"facility" binding:"required,max=120"`
	Owner       string    `json:"owner" binding:"required,max=120"`
	Category    string    `json:"category" binding:"required,max=80"`
	RiskLevel   string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue float64   `json:"metricValue"`
	MetricUnit  string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt time.Time `json:"effectiveAt" binding:"required"`
	Evidence    string    `json:"evidence" binding:"max=2000"`
	RelatedCode string    `json:"relatedCode" binding:"max=64"`
}

type UpdateVesselCall struct {
	ExpectedVersion uint      `json:"expectedVersion" binding:"required"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
}
