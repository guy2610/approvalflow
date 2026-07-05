package domain

import "time"

const (
	PubSubName              = "pubsub"
	TopicSubmissionReceived = "submission.received"
	TopicPaymentRequested   = "payment.requested"
	TopicApprovalRequired   = "approval.required"
)

type SubmissionReceivedEvent struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	CorrelationID string    `json:"correlation_id"`
	TrackingID    string    `json:"tracking_id"`
	InvoiceID     string    `json:"invoice_id,omitempty"`
	OccurredAtUTC time.Time `json:"occurred_at_utc"`
}

type PaymentRequestedEvent struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	CorrelationID string    `json:"correlation_id"`
	TrackingID    string    `json:"tracking_id"`
	InvoiceID     string    `json:"invoice_id,omitempty"`
	AmountUSD     float64   `json:"amount_usd"`
	OccurredAtUTC time.Time `json:"occurred_at_utc"`
}

type ApprovalRequiredEvent struct {
	EventID          string    `json:"event_id"`
	EventType        string    `json:"event_type"`
	CorrelationID    string    `json:"correlation_id"`
	TrackingID       string    `json:"tracking_id"`
	InvoiceID        string    `json:"invoice_id,omitempty"`
	AmountUSD        float64   `json:"amount_usd"`
	Reason           string    `json:"reason"`
	Violations       []string  `json:"violations"`
	AgentRecommended string    `json:"agent_recommended,omitempty"`
	AgentConfidence  float64   `json:"agent_confidence,omitempty"`
	AgentCitedRules  []string  `json:"agent_cited_rules,omitempty"`
	OccurredAtUTC    time.Time `json:"occurred_at_utc"`
}
