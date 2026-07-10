package domain

import "time"

type AnalyticsSummary struct {
	TotalSubmissions       int       `json:"total_submissions"`
	CompletedSubmissions   int       `json:"completed_submissions"`
	AutoApprovedCount      int       `json:"auto_approved_count"`
	HumanApprovedCount     int       `json:"human_approved_count"`
	HumanReviewCount       int       `json:"human_review_count"`
	RejectedCount          int       `json:"rejected_count"`
	PaymentFailedCount     int       `json:"payment_failed_count"`
	AutoApprovedAmountUSD  float64   `json:"auto_approved_amount_usd"`
	HumanApprovedAmountUSD float64   `json:"human_approved_amount_usd"`
	HumanReviewRate        float64   `json:"human_review_rate"`
	AutoApprovalRate       float64   `json:"auto_approval_rate"`
	UpdatedAtUTC           time.Time `json:"updated_at_utc"`
}
