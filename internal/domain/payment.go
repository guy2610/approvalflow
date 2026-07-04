package domain

import "time"

type PaymentStatus string

const (
	PaymentSucceeded PaymentStatus = "SUCCEEDED"
	PaymentFailed    PaymentStatus = "FAILED"
)

type PaymentRecord struct {
	PaymentID      string        `json:"payment_id"`
	TrackingID     string        `json:"tracking_id"`
	InvoiceID      string        `json:"invoice_id,omitempty"`
	AmountUSD      float64       `json:"amount_usd"`
	Status         PaymentStatus `json:"status"`
	Reason         string        `json:"reason"`
	IdempotencyKey string        `json:"idempotency_key"`
	CorrelationID  string        `json:"correlation_id"`
	CreatedAtUTC   time.Time     `json:"created_at_utc"`
	UpdatedAtUTC   time.Time     `json:"updated_at_utc"`
}
