package main

import (
	"testing"

	"approvalflow/internal/domain"
)

func TestApprovalKey(t *testing.T) {
	got := approvalKey("sub_123")
	want := "approval:sub_123"

	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestApprovalStatusConstants(t *testing.T) {
	tests := map[domain.ApprovalStatus]string{
		domain.ApprovalPending:     "PENDING",
		domain.ApprovalApproved:    "APPROVED",
		domain.ApprovalRejected:    "REJECTED",
		domain.ApprovalRequestInfo: "REQUEST_INFO",
	}

	for got, want := range tests {
		if string(got) != want {
			t.Fatalf("expected approval status %s, got %s", want, got)
		}
	}
}

func TestReopenApprovalItemResetsActionAndUsesLatestDecisionContext(t *testing.T) {
	existing := domain.ApprovalItem{
		TrackingID:    "sub_123",
		InvoiceID:     "INV-1003",
		AmountUSD:     1820,
		Status:        domain.ApprovalRequestInfo,
		Reason:        "More information required.",
		ActionBy:      "approver@example.com",
		ActionReason:  "Provide client name.",
		CorrelationID: "corr-123",
	}

	event := domain.ApprovalRequiredEvent{
		TrackingID:       "sub_123",
		InvoiceID:        "INV-1003",
		AmountUSD:        1820,
		Reason:           "Still above autonomy ceiling.",
		Violations:       []string{"AUTONOMY-CEILING"},
		AgentRecommended: "human_review",
		AgentConfidence:  0.92,
		AgentCitedRules:  []string{"AUTONOMY-CEILING"},
		CorrelationID:    "corr-123",
	}

	got := reopenApprovalItem(existing, event)

	if got.Status != domain.ApprovalPending {
		t.Fatalf("expected reopened approval to be pending, got %s", got.Status)
	}

	if got.ActionBy != "" || got.ActionReason != "" {
		t.Fatalf("expected previous approval action fields to be cleared")
	}

	if got.Reason != event.Reason {
		t.Fatalf("expected latest decision reason")
	}

	if len(got.Violations) != 1 || got.Violations[0] != "AUTONOMY-CEILING" {
		t.Fatalf("expected latest violations, got %v", got.Violations)
	}

	if got.AgentRecommended != event.AgentRecommended {
		t.Fatalf("expected latest agent recommendation")
	}

	if got.UpdatedAtUTC.IsZero() {
		t.Fatalf("expected updated timestamp")
	}
}
