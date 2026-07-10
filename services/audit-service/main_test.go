package main

import (
	"testing"
	"time"

	"approvalflow/internal/domain"
)

func TestAppendIfMissingAddsMissingCandidate(t *testing.T) {
	ids := []string{"audit-1", "audit-2"}

	got := appendIfMissing(ids, "audit-3")

	if len(got) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(got))
	}

	if got[2] != "audit-3" {
		t.Fatalf("expected audit-3 at the end, got %s", got[2])
	}
}

func TestAppendIfMissingKeepsExistingCandidateOnce(t *testing.T) {
	ids := []string{"audit-1", "audit-2"}

	got := appendIfMissing(ids, "audit-2")

	if len(got) != 2 {
		t.Fatalf("expected duplicate candidate not to be appended, got %d ids", len(got))
	}
}

func TestAuditKeys(t *testing.T) {
	if got := auditEventKey("evt-123"); got != "audit:event:evt-123" {
		t.Fatalf("unexpected audit event key: %s", got)
	}

	if got := auditIndexKey("corr-123"); got != "audit:index:corr-123" {
		t.Fatalf("unexpected audit index key: %s", got)
	}
}

func TestApplyAuditEventToAnalyticsTracksMainJourneys(t *testing.T) {
	now := time.Now().UTC()
	state := newAnalyticsState()

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-accepted",
		TrackingID:    "sub-1",
		Action:        "submission_accepted",
		OccurredAtUTC: now,
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-decision",
		TrackingID:    "sub-1",
		Action:        "decision_produced",
		Outcome:       string(domain.RouteAutoApprove),
		OccurredAtUTC: now,
		Fields: map[string]any{
			"route":      string(domain.RouteAutoApprove),
			"amount_usd": 125.50,
		},
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-paid",
		TrackingID:    "sub-1",
		Action:        "payment_succeeded",
		OccurredAtUTC: now,
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-2-accepted",
		TrackingID:    "sub-2",
		Action:        "submission_accepted",
		OccurredAtUTC: now,
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-2-decision",
		TrackingID:    "sub-2",
		Action:        "decision_produced",
		Outcome:       string(domain.RouteHumanReview),
		OccurredAtUTC: now,
		Fields: map[string]any{
			"route":      string(domain.RouteHumanReview),
			"amount_usd": 1820.0,
		},
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-2-human-approved",
		TrackingID:    "sub-2",
		Action:        "human_approved",
		OccurredAtUTC: now,
		Fields: map[string]any{
			"amount_usd": 1820.0,
		},
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-2-paid",
		TrackingID:    "sub-2",
		Action:        "payment_succeeded",
		OccurredAtUTC: now,
	})

	got := state.Summary

	if got.TotalSubmissions != 2 {
		t.Fatalf("expected 2 submissions, got %d", got.TotalSubmissions)
	}
	if got.CompletedSubmissions != 2 {
		t.Fatalf("expected 2 completed submissions, got %d", got.CompletedSubmissions)
	}
	if got.AutoApprovedCount != 1 {
		t.Fatalf("expected 1 auto approval, got %d", got.AutoApprovedCount)
	}
	if got.HumanReviewCount != 1 {
		t.Fatalf("expected 1 human review, got %d", got.HumanReviewCount)
	}
	if got.HumanApprovedCount != 1 {
		t.Fatalf("expected 1 human approval, got %d", got.HumanApprovedCount)
	}
	if got.AutoApprovedAmountUSD != 125.50 {
		t.Fatalf("unexpected auto-approved amount: %.2f", got.AutoApprovedAmountUSD)
	}
	if got.HumanApprovedAmountUSD != 1820.0 {
		t.Fatalf("unexpected human-approved amount: %.2f", got.HumanApprovedAmountUSD)
	}
	if got.AutoApprovalRate != 0.5 {
		t.Fatalf("expected auto approval rate 0.5, got %f", got.AutoApprovalRate)
	}
	if got.HumanReviewRate != 0.5 {
		t.Fatalf("expected human review rate 0.5, got %f", got.HumanReviewRate)
	}
}

func TestApplyAuditEventToAnalyticsIsIdempotent(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-accepted",
		TrackingID:    "sub-1",
		Action:        "submission_accepted",
		OccurredAtUTC: time.Now().UTC(),
	}

	state := applyAuditEventToAnalytics(newAnalyticsState(), event)
	state = applyAuditEventToAnalytics(state, event)

	if state.Summary.TotalSubmissions != 1 {
		t.Fatalf("expected duplicate event to be counted once, got %d", state.Summary.TotalSubmissions)
	}
}

func TestApplyAuditEventToAnalyticsCountsHumanReviewOnceAcrossReevaluation(t *testing.T) {
	now := time.Now().UTC()
	state := newAnalyticsState()

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-decision-r1",
		TrackingID:    "sub-1",
		Action:        "decision_produced",
		Outcome:       string(domain.RouteHumanReview),
		OccurredAtUTC: now,
		Fields: map[string]any{
			"route": string(domain.RouteHumanReview),
		},
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-decision-r2",
		TrackingID:    "sub-1",
		Action:        "decision_produced",
		Outcome:       string(domain.RouteHumanReview),
		OccurredAtUTC: now.Add(time.Minute),
		Fields: map[string]any{
			"route": string(domain.RouteHumanReview),
		},
	})

	if state.Summary.HumanReviewCount != 1 {
		t.Fatalf("expected one unique human review submission, got %d", state.Summary.HumanReviewCount)
	}
}

func TestApplyAuditEventToAnalyticsTracksFailureAndRejection(t *testing.T) {
	now := time.Now().UTC()
	state := newAnalyticsState()

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-1-failed",
		TrackingID:    "sub-1",
		Action:        "payment_failed_compensated",
		OccurredAtUTC: now,
	})

	state = applyAuditEventToAnalytics(state, domain.AuditEvent{
		EventID:       "audit-sub-2-rejected",
		TrackingID:    "sub-2",
		Action:        "human_rejected",
		OccurredAtUTC: now,
	})

	if state.Summary.PaymentFailedCount != 1 {
		t.Fatalf("expected 1 payment failure, got %d", state.Summary.PaymentFailedCount)
	}
	if state.Summary.RejectedCount != 1 {
		t.Fatalf("expected 1 rejection, got %d", state.Summary.RejectedCount)
	}
	if state.Summary.CompletedSubmissions != 2 {
		t.Fatalf("expected 2 completed submissions, got %d", state.Summary.CompletedSubmissions)
	}
}

func TestRefreshAnalyticsRatesHandlesEmptySummary(t *testing.T) {
	state := refreshAnalyticsRates(newAnalyticsState())

	if state.Summary.AutoApprovalRate != 0 {
		t.Fatalf("expected zero auto approval rate")
	}
	if state.Summary.HumanReviewRate != 0 {
		t.Fatalf("expected zero human review rate")
	}
}
