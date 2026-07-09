package policy

import (
	"fmt"

	"approvalflow/internal/domain"
)

const AutonomyBudgetRuleID = "AUTONOMY-BUDGET"

type AutonomyBudgetContext struct {
	SubmitterApprovedTodayUSD float64
	VendorApprovedTodayUSD    float64
}

func ApplyCumulativeAutonomyBudget(decision domain.DecisionResult, cfg Config, exposure AutonomyBudgetContext) domain.DecisionResult {
	cfg = normalizeConfig(cfg)

	if !cfg.CumulativeAutonomyEnabled {
		return decision
	}

	if decision.Route != domain.RouteAutoApprove {
		return decision
	}

	violations := make([]domain.PolicyViolation, 0)

	nextSubmitterTotal := exposure.SubmitterApprovedTodayUSD + decision.AmountUSD
	if nextSubmitterTotal > cfg.MaxDailyAutoApprovedPerSubmitterUSD {
		violations = append(violations, domain.PolicyViolation{
			RuleID: AutonomyBudgetRuleID,
			Message: fmt.Sprintf(
				"Daily submitter auto-approval exposure would become %.2f USD, above limit %.2f USD.",
				nextSubmitterTotal,
				cfg.MaxDailyAutoApprovedPerSubmitterUSD,
			),
		})
	}

	nextVendorTotal := exposure.VendorApprovedTodayUSD + decision.AmountUSD
	if nextVendorTotal > cfg.MaxDailyAutoApprovedPerVendorUSD {
		violations = append(violations, domain.PolicyViolation{
			RuleID: AutonomyBudgetRuleID,
			Message: fmt.Sprintf(
				"Daily vendor auto-approval exposure would become %.2f USD, above limit %.2f USD.",
				nextVendorTotal,
				cfg.MaxDailyAutoApprovedPerVendorUSD,
			),
		})
	}

	if len(violations) == 0 {
		return decision
	}

	decision.Route = domain.RouteHumanReview
	decision.Violations = append(decision.Violations, violations...)
	decision.Reason = summarizeHumanReview(decision.Violations)

	return decision
}
