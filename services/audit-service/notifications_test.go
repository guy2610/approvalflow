package main

import (
	"testing"
	"time"

	"approvalflow/internal/domain"
)

func TestNotificationFromPaymentSucceeded(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-paid",
		TrackingID:    "sub-1",
		InvoiceID:     "INV-1001",
		CorrelationID: "corr-1",
		Action:        "payment_succeeded",
		OccurredAtUTC: time.Now().UTC(),
	}

	got, ok := notificationFromAuditEvent(event)
	if !ok {
		t.Fatalf("expected payment success to create notification")
	}

	if got.Status != domain.SubmissionPaid {
		t.Fatalf("expected PAID status, got %s", got.Status)
	}

	if got.TrackingID != event.TrackingID {
		t.Fatalf("expected tracking id %s, got %s", event.TrackingID, got.TrackingID)
	}

	if got.Acknowledged {
		t.Fatalf("new notification must not be acknowledged")
	}
}

func TestNotificationFromPaymentFailure(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-failed",
		TrackingID:    "sub-1",
		Action:        "payment_failed_compensated",
		OccurredAtUTC: time.Now().UTC(),
	}

	got, ok := notificationFromAuditEvent(event)
	if !ok {
		t.Fatalf("expected payment failure to create notification")
	}

	if got.Status != domain.SubmissionPaymentFailed {
		t.Fatalf("expected PAYMENT_FAILED, got %s", got.Status)
	}
}

func TestNotificationFromHumanRejection(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-rejected",
		TrackingID:    "sub-1",
		Action:        "human_rejected",
		OccurredAtUTC: time.Now().UTC(),
	}

	got, ok := notificationFromAuditEvent(event)
	if !ok {
		t.Fatalf("expected human rejection to create notification")
	}

	if got.Status != domain.SubmissionRejectedByHuman {
		t.Fatalf("expected REJECTED_BY_HUMAN, got %s", got.Status)
	}
}

func TestNotificationFromDeterministicRejection(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-decision",
		TrackingID:    "sub-1",
		Action:        "decision_produced",
		Outcome:       string(domain.RouteReject),
		OccurredAtUTC: time.Now().UTC(),
		Fields: map[string]any{
			"route": string(domain.RouteReject),
		},
	}

	got, ok := notificationFromAuditEvent(event)
	if !ok {
		t.Fatalf("expected deterministic rejection to create notification")
	}

	if got.Status != domain.SubmissionRejected {
		t.Fatalf("expected REJECTED, got %s", got.Status)
	}
}

func TestNotificationIgnoresNonTerminalEvents(t *testing.T) {
	event := domain.AuditEvent{
		EventID:       "audit-sub-1-review",
		TrackingID:    "sub-1",
		Action:        "approval_required_published",
		OccurredAtUTC: time.Now().UTC(),
	}

	if _, ok := notificationFromAuditEvent(event); ok {
		t.Fatalf("expected non-terminal event not to create notification")
	}
}

func TestNotificationStateKey(t *testing.T) {
	if got := notificationStateKey("sub-123"); got != "notification:sub-123" {
		t.Fatalf("unexpected notification state key: %s", got)
	}
}
