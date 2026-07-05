package domain

import "time"

type AuditEvent struct {
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	CorrelationID string         `json:"correlation_id"`
	TrackingID    string         `json:"tracking_id,omitempty"`
	InvoiceID     string         `json:"invoice_id,omitempty"`
	Service       string         `json:"service"`
	Action        string         `json:"action"`
	Outcome       string         `json:"outcome"`
	Reason        string         `json:"reason,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
	OccurredAtUTC time.Time      `json:"occurred_at_utc"`
}

type AuditTrailResponse struct {
	CorrelationID string       `json:"correlation_id"`
	Events        []AuditEvent `json:"events"`
}
