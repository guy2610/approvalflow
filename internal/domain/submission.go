package domain

import "time"

type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unitPrice"`
}

type SubmissionRequest struct {
	ID             string     `json:"id,omitempty"`
	Submitter      string     `json:"submitter"`
	Department     string     `json:"department"`
	Vendor         string     `json:"vendor"`
	VendorKnown    bool       `json:"vendorKnown"`
	InvoiceNumber  string     `json:"invoiceNumber"`
	Currency       string     `json:"currency"`
	Category       string     `json:"category"`
	Attendees      *int       `json:"attendees,omitempty"`
	LineItems      []LineItem `json:"lineItems"`
	TaxAmount      float64    `json:"taxAmount"`
	Total          float64    `json:"total"`
	ReceiptPresent bool       `json:"receiptPresent"`
	Date           string     `json:"date"`
	Notes          string     `json:"notes"`
}

type SubmissionStatus string

const (
	SubmissionAccepted               SubmissionStatus = "ACCEPTED"
	SubmissionDuplicate              SubmissionStatus = "DUPLICATE"
	SubmissionProcessing             SubmissionStatus = "PROCESSING"
	SubmissionAutoApprovedPendingPay SubmissionStatus = "AUTO_APPROVED_PENDING_PAYMENT"
	SubmissionPaymentPending         SubmissionStatus = "PAYMENT_PENDING"
	SubmissionPaid                   SubmissionStatus = "PAID"
	SubmissionPaymentFailed          SubmissionStatus = "PAYMENT_FAILED"
	SubmissionHumanReviewRequired    SubmissionStatus = "HUMAN_REVIEW_REQUIRED"
	SubmissionApprovedByHuman        SubmissionStatus = "APPROVED_BY_HUMAN"
	SubmissionRejectedByHuman        SubmissionStatus = "REJECTED_BY_HUMAN"
	SubmissionInfoRequested          SubmissionStatus = "INFO_REQUESTED"
	SubmissionRejected               SubmissionStatus = "REJECTED"
)

type SubmissionRecord struct {
	TrackingID        string            `json:"tracking_id"`
	OriginalInvoiceID string            `json:"original_invoice_id,omitempty"`
	CorrelationID     string            `json:"correlation_id,omitempty"`
	RevisionNumber    int               `json:"revision_number"`
	Status            SubmissionStatus  `json:"status"`
	Reason            string            `json:"reason"`
	Duplicate         bool              `json:"duplicate"`
	DuplicateOf       string            `json:"duplicate_of,omitempty"`
	Request           SubmissionRequest `json:"request"`
	CreatedAtUTC      time.Time         `json:"created_at_utc"`
	UpdatedAtUTC      time.Time         `json:"updated_at_utc"`
}

type SubmissionResponse struct {
	TrackingID  string           `json:"tracking_id"`
	Status      SubmissionStatus `json:"status"`
	Reason      string           `json:"reason"`
	Duplicate   bool             `json:"duplicate"`
	DuplicateOf string           `json:"duplicate_of,omitempty"`
}
