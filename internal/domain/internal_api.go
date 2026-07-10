package domain

type ApplyDecisionRequest struct {
	Status        SubmissionStatus `json:"status"`
	Reason        string           `json:"reason"`
	DecisionRoute DecisionRoute    `json:"decision_route"`
}

type ApplyPaymentRequest struct {
	Status    SubmissionStatus `json:"status"`
	Reason    string           `json:"reason"`
	PaymentID string           `json:"payment_id"`
}

type ApplyApprovalRequest struct {
	Status SubmissionStatus `json:"status"`
	Reason string           `json:"reason"`
	Actor  string           `json:"actor"`
}

type AdditionalInfoRequest struct {
	Notes          *string `json:"notes,omitempty"`
	ReceiptPresent *bool   `json:"receiptPresent,omitempty"`
	Attendees      *int    `json:"attendees,omitempty"`
}
