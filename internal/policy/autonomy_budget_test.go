package policy

import (
	"testing"

	"approvalflow/internal/domain"
)

func TestApplyCumulativeAutonomyBudgetKeepsAutoApproveUnderLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CumulativeAutonomyEnabled = true
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 500
	cfg.MaxDailyAutoApprovedPerVendorUSD = 500

	decision := domain.DecisionResult{
		Route:     domain.RouteAutoApprove,
		AmountUSD: 100,
	}

	got := ApplyCumulativeAutonomyBudget(decision, cfg, AutonomyBudgetContext{
		SubmitterApprovedTodayUSD: 100,
		VendorApprovedTodayUSD:    200,
	})

	if got.Route != domain.RouteAutoApprove {
		t.Fatalf("expected auto approve, got %s with violations %+v", got.Route, got.Violations)
	}
}

func TestApplyCumulativeAutonomyBudgetForcesHumanReviewWhenSubmitterLimitExceeded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CumulativeAutonomyEnabled = true
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 250
	cfg.MaxDailyAutoApprovedPerVendorUSD = 1000

	decision := domain.DecisionResult{
		Route:     domain.RouteAutoApprove,
		AmountUSD: 100,
	}

	got := ApplyCumulativeAutonomyBudget(decision, cfg, AutonomyBudgetContext{
		SubmitterApprovedTodayUSD: 200,
		VendorApprovedTodayUSD:    0,
	})

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected human review, got %s", got.Route)
	}

	if !hasViolation(got, AutonomyBudgetRuleID) {
		t.Fatalf("expected AUTONOMY-BUDGET violation, got %+v", got.Violations)
	}
}

func TestApplyCumulativeAutonomyBudgetIgnoresNonAutoApproveRoutes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CumulativeAutonomyEnabled = true
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 1
	cfg.MaxDailyAutoApprovedPerVendorUSD = 1

	decision := domain.DecisionResult{
		Route:     domain.RouteHumanReview,
		AmountUSD: 100,
	}

	got := ApplyCumulativeAutonomyBudget(decision, cfg, AutonomyBudgetContext{
		SubmitterApprovedTodayUSD: 1000,
		VendorApprovedTodayUSD:    1000,
	})

	if got.Route != domain.RouteHumanReview {
		t.Fatalf("expected route to stay human review, got %s", got.Route)
	}

	if hasViolation(got, AutonomyBudgetRuleID) {
		t.Fatalf("did not expect AUTONOMY-BUDGET on already-human route, got %+v", got.Violations)
	}
}
