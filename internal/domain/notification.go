package domain

import "time"

type Notification struct {
	NotificationID    string           `json:"notification_id"`
	TrackingID        string           `json:"tracking_id"`
	InvoiceID         string           `json:"invoice_id,omitempty"`
	CorrelationID     string           `json:"correlation_id"`
	Status            SubmissionStatus `json:"status"`
	Message           string           `json:"message"`
	Acknowledged      bool             `json:"acknowledged"`
	CreatedAtUTC      time.Time        `json:"created_at_utc"`
	AcknowledgedAtUTC *time.Time       `json:"acknowledged_at_utc,omitempty"`
}
