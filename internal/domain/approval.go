package domain

import "time"

type ApprovalStatus string

const (
	ApprovalPending     ApprovalStatus = "PENDING"
	ApprovalApproved    ApprovalStatus = "APPROVED"
	ApprovalRejected    ApprovalStatus = "REJECTED"
	ApprovalRequestInfo ApprovalStatus = "REQUEST_INFO"
)

type ApprovalItem struct {
	TrackingID    string         `json:"tracking_id"`
	InvoiceID     string         `json:"invoice_id,omitempty"`
	AmountUSD     float64        `json:"amount_usd"`
	Status        ApprovalStatus `json:"status"`
	Reason        string         `json:"reason"`
	Violations    []string       `json:"violations"`
	CorrelationID string         `json:"correlation_id"`
	CreatedAtUTC  time.Time      `json:"created_at_utc"`
	UpdatedAtUTC  time.Time      `json:"updated_at_utc"`
	ActionBy      string         `json:"action_by,omitempty"`
	ActionReason  string         `json:"action_reason,omitempty"`
}

type ApprovalActionRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type ApprovalListResponse struct {
	Items []ApprovalItem `json:"items"`
}
