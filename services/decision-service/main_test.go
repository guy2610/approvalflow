package main

import (
	"testing"
	"time"

	"approvalflow/internal/domain"
)

func TestStatusFromDecision(t *testing.T) {
	tests := []struct {
		name  string
		route domain.DecisionRoute
		want  domain.SubmissionStatus
	}{
		{
			name:  "auto approve",
			route: domain.RouteAutoApprove,
			want:  domain.SubmissionAutoApprovedPendingPay,
		},
		{
			name:  "human review",
			route: domain.RouteHumanReview,
			want:  domain.SubmissionHumanReviewRequired,
		},
		{
			name:  "reject",
			route: domain.RouteReject,
			want:  domain.SubmissionRejected,
		},
		{
			name:  "unknown route fails closed",
			route: domain.DecisionRoute("unexpected"),
			want:  domain.SubmissionHumanReviewRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusFromDecision(tt.route); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestViolationIDs(t *testing.T) {
	violations := []domain.PolicyViolation{
		{RuleID: "GLOBAL-RECEIPT", Message: "receipt required"},
		{RuleID: "AUTONOMY-CEILING", Message: "too large"},
	}

	got := violationIDs(violations)
	want := []string{"GLOBAL-RECEIPT", "AUTONOMY-CEILING"}

	if len(got) != len(want) {
		t.Fatalf("expected %d ids, got %d", len(want), len(got))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected id[%d]=%s, got %s", i, want[i], got[i])
		}
	}
}

func TestAutonomyDateKeyUsesSubmissionDateWhenPresent(t *testing.T) {
	decidedAt := time.Date(2026, 7, 9, 12, 30, 0, 0, time.UTC)

	got := autonomyDateKey("2026-05-30", decidedAt)
	if got != "2026-05-30" {
		t.Fatalf("expected submission date, got %s", got)
	}
}

func TestAutonomyDateKeyFallsBackToDecisionDate(t *testing.T) {
	decidedAt := time.Date(2026, 7, 9, 12, 30, 0, 0, time.UTC)

	got := autonomyDateKey("", decidedAt)
	if got != "2026-07-09" {
		t.Fatalf("expected decision date fallback, got %s", got)
	}
}
