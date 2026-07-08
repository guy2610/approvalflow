package policy

import (
	"fmt"
	"math"
	"strings"
	"time"

	"approvalflow/internal/domain"
)

const RouterVersion = "deterministic-policy-router-v1"

func Evaluate(req domain.SubmissionRequest, cfg Config) domain.DecisionResult {
	cfg = normalizeConfig(cfg)

	amountUSD := convertToUSD(req.Total, req.Currency, cfg)
	confidence := estimateConfidence(req, cfg)

	violations := make([]domain.PolicyViolation, 0)

	if isAlcoholOnly(req) {
		violations = append(violations, violation("MEAL-03", "Alcohol-only receipts are not reimbursable."))
		return result(domain.RouteReject, confidence, amountUSD, violations, "Rejected because the receipt is alcohol-only.")
	}

	if boolValue(cfg.MathMismatchRequiresReview, true) && !mathReconciles(req) {
		violations = append(violations, violation("GLOBAL-MATH", "Line items plus tax do not reconcile to total."))
	}

	if boolValue(cfg.MissingReceiptRequiresReview, true) && amountUSD > cfg.ReceiptRequiredAboveUSD && !req.ReceiptPresent {
		violations = append(violations, violation("GLOBAL-RECEIPT", fmt.Sprintf("Receipt is required for expenses over %.2f USD.", cfg.ReceiptRequiredAboveUSD)))
	}

	if boolValue(cfg.NewVendorRequiresReview, true) && !req.VendorKnown {
		violations = append(violations, violation("GLOBAL-VENDOR", "New or unknown vendor requires human review."))
	}

	if boolValue(cfg.ForeignCurrencyRequiresReview, true) &&
		strings.ToUpper(req.Currency) != "USD" &&
		(amountUSD > cfg.AutonomyCeilingUSD || amountUSD > cfg.ForeignCurrencyHardStopUSD) {
		violations = append(violations, violation("GLOBAL-FX", "Foreign-currency item crosses a hard-stop threshold."))
	}

	switch strings.ToLower(strings.TrimSpace(req.Category)) {
	case "meals":
		violations = append(violations, evaluateMeals(req, cfg)...)
	case "travel":
		violations = append(violations, evaluateTravel(req, cfg)...)
	case "saas":
		violations = append(violations, evaluateSaaS(req, cfg)...)
	case "hardware":
		violations = append(violations, evaluateHardware(req, cfg)...)
	default:
		confidence = math.Min(confidence, cfg.UnknownCategoryMaxConfidence)
	}

	if amountUSD > cfg.AutonomyCeilingUSD {
		violations = append(violations, violation("AUTONOMY-CEILING", fmt.Sprintf("Amount %.2f USD is above autonomy ceiling %.2f USD.", amountUSD, cfg.AutonomyCeilingUSD)))
	}

	if confidence < cfg.MinConfidence {
		violations = append(violations, violation("AUTONOMY-CONFIDENCE", fmt.Sprintf("Confidence %.2f is below required %.2f.", confidence, cfg.MinConfidence)))
	}

	if len(violations) > 0 {
		return result(domain.RouteHumanReview, confidence, amountUSD, violations, summarizeHumanReview(violations))
	}

	return result(domain.RouteAutoApprove, confidence, amountUSD, nil, "Auto-approved by deterministic policy router: under autonomy ceiling, policy-compliant, known vendor, receipt present when required, and math reconciles.")
}

func evaluateMeals(req domain.SubmissionRequest, cfg Config) []domain.PolicyViolation {
	var violations []domain.PolicyViolation

	if req.Attendees == nil {
		violations = append(violations, violation("MEAL-01", "Meal submissions must include attendee count."))
		return violations
	}

	if *req.Attendees <= 0 {
		violations = append(violations, violation("MEAL-01", "Attendee count must be positive."))
		return violations
	}

	perAttendee := req.Total / float64(*req.Attendees)
	if perAttendee > cfg.MealMaxPerAttendeeUSD && req.Total <= cfg.MealClientReviewAboveUSD {
		violations = append(violations, violation("MEAL-01", fmt.Sprintf("Meal amount %.2f per attendee exceeds %.2f USD.", perAttendee, cfg.MealMaxPerAttendeeUSD)))
	}

	if req.Total > cfg.MealClientReviewAboveUSD && !hasClientEntertainmentInfo(req) {
		violations = append(violations, violation("MEAL-02", fmt.Sprintf("Client entertainment over %.2f USD requires business justification and client name.", cfg.MealClientReviewAboveUSD)))
	}

	return violations
}

func evaluateTravel(req domain.SubmissionRequest, cfg Config) []domain.PolicyViolation {
	var violations []domain.PolicyViolation

	if req.Total > cfg.TravelManagerReviewAboveUSD {
		violations = append(violations, violation("TRAVEL-02", fmt.Sprintf("Single travel expense over %.2f USD requires manager approval.", cfg.TravelManagerReviewAboveUSD)))
	}

	text := strings.ToLower(req.Notes + " " + lineDescriptions(req))
	if strings.Contains(text, "business class") || strings.Contains(text, "first class") || strings.Contains(text, "first-class") {
		violations = append(violations, violation("TRAVEL-03", "First/business-class travel requires approval."))
	}

	return violations
}

func evaluateSaaS(req domain.SubmissionRequest, cfg Config) []domain.PolicyViolation {
	if req.Total > cfg.SaaSMonthlyEligibleLimitUSD {
		return []domain.PolicyViolation{
			violation("SAAS-01", fmt.Sprintf("SaaS subscriptions are policy-eligible up to %.2f USD/month.", cfg.SaaSMonthlyEligibleLimitUSD)),
		}
	}
	return nil
}

func evaluateHardware(req domain.SubmissionRequest, cfg Config) []domain.PolicyViolation {
	if req.Total > cfg.HardwareCapitalReviewAboveUSD {
		return []domain.PolicyViolation{
			violation("HW-02", fmt.Sprintf("Hardware over %.2f USD is a capital expense and requires human approval.", cfg.HardwareCapitalReviewAboveUSD)),
		}
	}
	return nil
}

func mathReconciles(req domain.SubmissionRequest) bool {
	sum := req.TaxAmount
	for _, item := range req.LineItems {
		sum += item.Quantity * item.UnitPrice
	}
	return math.Abs(sum-req.Total) <= 0.01
}

func convertToUSD(amount float64, currency string, cfg Config) float64 {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	rate, ok := cfg.FXRates[currency]
	if !ok {
		return amount
	}
	return amount * rate
}

func estimateConfidence(req domain.SubmissionRequest, cfg Config) float64 {
	category := strings.ToLower(strings.TrimSpace(req.Category))
	if category == "other" || category == "" {
		return cfg.UnknownCategoryMaxConfidence
	}
	return 0.95
}

func isAlcoholOnly(req domain.SubmissionRequest) bool {
	text := strings.ToLower(req.Notes + " " + lineDescriptions(req))
	return strings.Contains(text, "alcohol-only")
}

func hasClientEntertainmentInfo(req domain.SubmissionRequest) bool {
	text := strings.ToLower(req.Notes)
	hasClient := strings.Contains(text, "client:")
	hasJustification := strings.Contains(text, "justification:")
	return hasClient && hasJustification
}

func lineDescriptions(req domain.SubmissionRequest) string {
	parts := make([]string, 0, len(req.LineItems))
	for _, item := range req.LineItems {
		parts = append(parts, item.Description)
	}
	return strings.Join(parts, " ")
}

func violation(ruleID string, message string) domain.PolicyViolation {
	return domain.PolicyViolation{
		RuleID:  ruleID,
		Message: message,
	}
}

func result(route domain.DecisionRoute, confidence float64, amountUSD float64, violations []domain.PolicyViolation, reason string) domain.DecisionResult {
	return domain.DecisionResult{
		Route:         route,
		Confidence:    confidence,
		AmountUSD:     round2(amountUSD),
		Violations:    violations,
		Reason:        reason,
		RouterVersion: RouterVersion,
		DecidedAtUTC:  time.Now().UTC(),
	}
}

func summarizeHumanReview(violations []domain.PolicyViolation) string {
	if len(violations) == 0 {
		return "Human review required."
	}

	ruleIDs := make([]string, 0, len(violations))
	for _, v := range violations {
		ruleIDs = append(ruleIDs, v.RuleID)
	}

	return "Human review required because deterministic policy rules fired: " + strings.Join(ruleIDs, ", ") + "."
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
