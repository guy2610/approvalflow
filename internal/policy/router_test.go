package policy

import (
	"encoding/json"
	"os"
	"testing"

	"approvalflow/internal/domain"
)

type sampleInvoices struct {
	Fixtures []domain.SubmissionRequest `json:"fixtures"`
}

func TestEvaluateCoreFixtures(t *testing.T) {
	fixtures := loadFixtures(t)
	cfg := ConfigFromEnv()

	tests := map[string]domain.DecisionRoute{
		"INV-1001": domain.RouteAutoApprove,
		"INV-1003": domain.RouteHumanReview,
		"INV-1015": domain.RouteReject,
		"INV-1018": domain.RouteHumanReview,
	}

	for id, expectedRoute := range tests {
		req := fixtures[id]
		got := Evaluate(req, cfg)

		if got.Route != expectedRoute {
			t.Fatalf("%s: expected route %s, got %s. reason=%s", id, expectedRoute, got.Route, got.Reason)
		}
	}
}

func TestEvaluateBlocksAboveCeilingEvenIfOtherwiseEligible(t *testing.T) {
	fixtures := loadFixtures(t)
	got := Evaluate(fixtures["INV-1013"], ConfigFromEnv())

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected human review, got %s", got.Route)
	}

	if !hasViolation(got, "AUTONOMY-CEILING") {
		t.Fatalf("expected AUTONOMY-CEILING violation, got %+v", got.Violations)
	}
}

func TestEvaluateDuplicateFixtureWouldAutoApproveWithoutDuplicateState(t *testing.T) {
	fixtures := loadFixtures(t)
	got := Evaluate(fixtures["INV-1007"], ConfigFromEnv())

	if got.Route != domain.RouteAutoApprove {
		t.Fatalf("router should not decide duplicate by itself; expected auto_approve before stateful duplicate layer, got %s", got.Route)
	}
}

func loadFixtures(t *testing.T) map[string]domain.SubmissionRequest {
	t.Helper()

	raw, err := os.ReadFile("../../data/sample-invoices.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}

	var parsed sampleInvoices
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}

	out := make(map[string]domain.SubmissionRequest)
	for _, fixture := range parsed.Fixtures {
		out[fixture.ID] = fixture
	}

	return out
}

func hasViolation(result domain.DecisionResult, ruleID string) bool {
	for _, violation := range result.Violations {
		if violation.RuleID == ruleID {
			return true
		}
	}
	return false
}
