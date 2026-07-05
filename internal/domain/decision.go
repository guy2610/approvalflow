package domain

import "time"

type DecisionRoute string

const (
	RouteAutoApprove DecisionRoute = "auto_approve"
	RouteHumanReview DecisionRoute = "human_review"
	RouteReject      DecisionRoute = "reject"
	RouteDuplicate   DecisionRoute = "duplicate"
)

type PolicyViolation struct {
	RuleID  string `json:"rule_id"`
	Message string `json:"message"`
}

type DecisionResult struct {
	TrackingID       string            `json:"tracking_id,omitempty"`
	InvoiceID        string            `json:"invoice_id,omitempty"`
	Route            DecisionRoute     `json:"route"`
	Confidence       float64           `json:"confidence"`
	AmountUSD        float64           `json:"amount_usd"`
	Violations       []PolicyViolation `json:"violations"`
	Reason           string            `json:"reason"`
	RouterVersion    string            `json:"router_version"`
	DecidedAtUTC     time.Time         `json:"decided_at_utc"`
	CorrelationID    string            `json:"correlation_id,omitempty"`
	AgentRecommended string            `json:"agent_recommended,omitempty"`
}
