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
