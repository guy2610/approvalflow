package policy

import (
	"testing"

	"approvalflow/internal/domain"
)

func TestLowRiskMealAutoApproves(t *testing.T) {
	req := baseRequest()
	req.ID = "TEST-LOW-RISK-MEAL"
	req.Category = "meals"
	req.Total = 42
	req.TaxAmount = 0
	req.LineItems = []domain.LineItem{
		{Description: "Team lunch", Quantity: 1, UnitPrice: 42},
	}
	req.Attendees = intPtr(1)
	req.ReceiptPresent = true
	req.VendorKnown = true

	got := Evaluate(req, Config{
		AutonomyCeilingUSD: 250,
		MinConfidence:      0.80,
		FXRates:            map[string]float64{"USD": 1.0},
	})

	if got.Route != domain.RouteAutoApprove {
		t.Fatalf("expected auto approve, got %s with violations %+v", got.Route, got.Violations)
	}
}

func TestSaaSAbovePolicyCapRequiresHumanReview(t *testing.T) {
	req := baseRequest()
	req.ID = "TEST-SAAS-ABOVE-CAP"
	req.Category = "saas"
	req.Total = 220
	req.TaxAmount = 0
	req.LineItems = []domain.LineItem{
		{Description: "Monthly subscription", Quantity: 1, UnitPrice: 220},
	}
	req.ReceiptPresent = true
	req.VendorKnown = true

	got := Evaluate(req, Config{
		AutonomyCeilingUSD: 250,
		MinConfidence:      0.80,
		FXRates:            map[string]float64{"USD": 1.0},
	})

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected human review, got %s", got.Route)
	}

	if !hasViolation(got, "SAAS-01") {
		t.Fatalf("expected SAAS-01 violation, got %+v", got.Violations)
	}
}

func TestAlcoholOnlyReceiptRejects(t *testing.T) {
	req := baseRequest()
	req.ID = "TEST-ALCOHOL-ONLY"
	req.Category = "meals"
	req.Total = 30
	req.TaxAmount = 0
	req.LineItems = []domain.LineItem{
		{Description: "alcohol-only receipt", Quantity: 1, UnitPrice: 30},
	}
	req.Attendees = intPtr(1)
	req.ReceiptPresent = true
	req.VendorKnown = true
	req.Notes = "alcohol-only"

	got := Evaluate(req, Config{
		AutonomyCeilingUSD: 250,
		MinConfidence:      0.80,
		FXRates:            map[string]float64{"USD": 1.0},
	})

	if got.Route != domain.RouteReject {
		t.Fatalf("expected reject, got %s", got.Route)
	}

	if !hasViolation(got, "MEAL-03") {
		t.Fatalf("expected MEAL-03 violation, got %+v", got.Violations)
	}
}

func TestMissingReceiptRequiresHumanReview(t *testing.T) {
	req := baseRequest()
	req.ID = "TEST-MISSING-RECEIPT"
	req.Category = "meals"
	req.Total = 42
	req.TaxAmount = 0
	req.LineItems = []domain.LineItem{
		{Description: "Team lunch", Quantity: 1, UnitPrice: 42},
	}
	req.Attendees = intPtr(1)
	req.ReceiptPresent = false
	req.VendorKnown = true

	got := Evaluate(req, Config{
		AutonomyCeilingUSD: 250,
		MinConfidence:      0.80,
		FXRates:            map[string]float64{"USD": 1.0},
	})

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected human review, got %s", got.Route)
	}

	if !hasViolation(got, "GLOBAL-RECEIPT") {
		t.Fatalf("expected GLOBAL-RECEIPT violation, got %+v", got.Violations)
	}
}

func TestMathMismatchRequiresHumanReview(t *testing.T) {
	req := baseRequest()
	req.ID = "TEST-MATH-MISMATCH"
	req.Category = "meals"
	req.Total = 50
	req.TaxAmount = 0
	req.LineItems = []domain.LineItem{
		{Description: "Team lunch", Quantity: 1, UnitPrice: 42},
	}
	req.Attendees = intPtr(1)
	req.ReceiptPresent = true
	req.VendorKnown = true

	got := Evaluate(req, Config{
		AutonomyCeilingUSD: 250,
		MinConfidence:      0.80,
		FXRates:            map[string]float64{"USD": 1.0},
	})

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected human review, got %s", got.Route)
	}

	if !hasViolation(got, "GLOBAL-MATH") {
		t.Fatalf("expected GLOBAL-MATH violation, got %+v", got.Violations)
	}
}

func baseRequest() domain.SubmissionRequest {
	return domain.SubmissionRequest{
		ID:             "TEST-INVOICE",
		Vendor:         "Known Vendor",
		InvoiceNumber:  "TEST-001",
		Category:       "meals",
		Currency:       "USD",
		Total:          42,
		TaxAmount:      0,
		LineItems:      []domain.LineItem{{Description: "Item", Quantity: 1, UnitPrice: 42}},
		ReceiptPresent: true,
		VendorKnown:    true,
		Notes:          "",
	}
}

func intPtr(value int) *int {
	return &value
}
