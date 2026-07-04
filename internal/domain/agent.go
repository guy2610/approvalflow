package domain

type AgentRecommendationRequest struct {
	TrackingID    string            `json:"tracking_id"`
	CorrelationID string            `json:"correlation_id"`
	Submission    SubmissionRequest `json:"submission"`
}

type CitedRule struct {
	RuleID string `json:"rule_id"`
	Quote  string `json:"quote"`
}

type AgentRecommendation struct {
	RecommendedRoute string      `json:"recommended_route"`
	Confidence       float64     `json:"confidence"`
	Reason           string      `json:"reason"`
	CitedRules       []CitedRule `json:"cited_rules"`
	Provider         string      `json:"provider"`
}
