package main

import (
	"testing"
	"time"

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

func TestApprovalStatusForAction(t *testing.T) {
	tests := map[string]domain.ApprovalStatus{
		"approve":      domain.ApprovalApproved,
		"reject":       domain.ApprovalRejected,
		"request-info": domain.ApprovalRequestInfo,
	}

	for action, expected := range tests {
		got, ok := approvalStatusForAction(action)
		if !ok {
			t.Fatalf("expected action %s to be valid", action)
		}
		if got != expected {
			t.Fatalf(
				"expected %s for action %s, got %s",
				expected,
				action,
				got,
			)
		}
	}

	if _, ok := approvalStatusForAction("unknown"); ok {
		t.Fatalf("expected unknown action to be rejected")
	}
}

func TestCanReconcileApproveAcrossPartialFailureStates(t *testing.T) {
	allowed := []domain.SubmissionStatus{
		domain.SubmissionHumanReviewRequired,
		domain.SubmissionApprovedByHuman,
		domain.SubmissionPaymentPending,
		domain.SubmissionPaid,
		domain.SubmissionPaymentFailed,
	}

	for _, status := range allowed {
		if !canReconcileApprovalAction("approve", status) {
			t.Fatalf("expected approve retry to reconcile status %s", status)
		}
	}

	if canReconcileApprovalAction("approve", domain.SubmissionRejectedByHuman) {
		t.Fatalf("approve must not reconcile a rejected submission")
	}
}

func TestRejectAndRequestInfoReconciliationStates(t *testing.T) {
	if !canReconcileApprovalAction(
		"reject",
		domain.SubmissionRejectedByHuman,
	) {
		t.Fatalf("expected rejected submission to accept identical reject retry")
	}

	if !canReconcileApprovalAction(
		"request-info",
		domain.SubmissionInfoRequested,
	) {
		t.Fatalf("expected info-requested submission to accept identical retry")
	}

	if canReconcileApprovalAction(
		"request-info",
		domain.SubmissionProcessing,
	) {
		t.Fatalf("stale request-info retry must not move a resumed submission backwards")
	}
}

func TestApprovalPaymentPublishingDecision(t *testing.T) {
	if !shouldPublishApprovalPayment(
		domain.SubmissionApprovedByHuman,
	) {
		t.Fatalf("expected payment publish while approved submission awaits payment")
	}

	terminal := []domain.SubmissionStatus{
		domain.SubmissionPaymentPending,
		domain.SubmissionPaid,
		domain.SubmissionPaymentFailed,
	}

	for _, status := range terminal {
		if shouldPublishApprovalPayment(status) {
			t.Fatalf(
				"must not republish payment after submission reached %s",
				status,
			)
		}
	}
}

func TestApprovalEffectEventIDIsStableAndVersioned(t *testing.T) {
	firstTime := time.Date(
		2026,
		time.July,
		10,
		12,
		0,
		0,
		123,
		time.UTC,
	)
	secondTime := firstTime.Add(time.Second)

	firstItem := domain.ApprovalItem{
		TrackingID:   "sub-123",
		UpdatedAtUTC: firstTime,
	}
	retryItem := firstItem
	secondRevision := firstItem
	secondRevision.UpdatedAtUTC = secondTime

	first := approvalEffectEventID(firstItem, "audit_info_requested")
	retry := approvalEffectEventID(retryItem, "audit_info_requested")
	later := approvalEffectEventID(
		secondRevision,
		"audit_info_requested",
	)

	if first != retry {
		t.Fatalf("expected retry-stable event id")
	}

	if first == later {
		t.Fatalf("expected different event id for later approval cycle")
	}
}

func TestClassifyApprovalRequiredEventDetectsSameEventDuplicate(t *testing.T) {
	existing := domain.ApprovalItem{
		SourceEventID:  "evt-revision-2",
		RevisionNumber: 2,
		Status:         domain.ApprovalRequestInfo,
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, event); got != approvalEventDuplicate {
		t.Fatalf("expected duplicate, got %s", got)
	}
}

func TestClassifyApprovalRequiredEventRejectsOlderRevision(t *testing.T) {
	existing := domain.ApprovalItem{
		SourceEventID:  "evt-revision-3",
		RevisionNumber: 3,
		Status:         domain.ApprovalRequestInfo,
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, event); got != approvalEventStale {
		t.Fatalf("expected stale, got %s", got)
	}
}

func TestClassifyApprovalRequiredEventTreatsSameRevisionAsDuplicate(t *testing.T) {
	existing := domain.ApprovalItem{
		SourceEventID:  "evt-revision-2-original",
		RevisionNumber: 2,
		Status:         domain.ApprovalRequestInfo,
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2-redelivery",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, event); got != approvalEventDuplicate {
		t.Fatalf("expected duplicate, got %s", got)
	}
}

func TestClassifyApprovalRequiredEventReopensNewerRevision(t *testing.T) {
	existing := domain.ApprovalItem{
		SourceEventID:  "evt-revision-1",
		RevisionNumber: 1,
		Status:         domain.ApprovalRequestInfo,
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, event); got != approvalEventReopen {
		t.Fatalf("expected reopen, got %s", got)
	}
}

func TestClassifyApprovalRequiredEventDoesNotReopenCompletedApproval(t *testing.T) {
	existing := domain.ApprovalItem{
		SourceEventID:  "evt-revision-1",
		RevisionNumber: 1,
		Status:         domain.ApprovalApproved,
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, event); got != approvalEventIgnore {
		t.Fatalf("expected ignore, got %s", got)
	}
}

func TestClassifyApprovalRequiredEventSupportsLegacyRevisionZero(t *testing.T) {
	existing := domain.ApprovalItem{
		Status: domain.ApprovalRequestInfo,
	}

	initialEvent := domain.ApprovalRequiredEvent{
		EventID:        "evt-initial",
		RevisionNumber: 1,
	}

	if got := classifyApprovalRequiredEvent(existing, initialEvent); got != approvalEventDuplicate {
		t.Fatalf("expected legacy initial event to be duplicate, got %s", got)
	}

	revisedEvent := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
	}

	if got := classifyApprovalRequiredEvent(existing, revisedEvent); got != approvalEventReopen {
		t.Fatalf("expected revision 2 to reopen legacy item, got %s", got)
	}
}

func TestReopenApprovalItemStoresLatestEventMetadata(t *testing.T) {
	existing := domain.ApprovalItem{
		TrackingID:     "sub-123",
		SourceEventID:  "evt-revision-1",
		RevisionNumber: 1,
		Status:         domain.ApprovalRequestInfo,
		ActionBy:       "approver@example.local",
		ActionReason:   "Need more information.",
	}

	event := domain.ApprovalRequiredEvent{
		EventID:        "evt-revision-2",
		RevisionNumber: 2,
		TrackingID:     "sub-123",
		InvoiceID:      "INV-1003",
		AmountUSD:      1820,
		Reason:         "Still requires human review.",
	}

	got := reopenApprovalItem(existing, event)

	if got.SourceEventID != event.EventID {
		t.Fatalf("expected source event %q, got %q", event.EventID, got.SourceEventID)
	}

	if got.RevisionNumber != 2 {
		t.Fatalf("expected revision 2, got %d", got.RevisionNumber)
	}

	if got.Status != domain.ApprovalPending {
		t.Fatalf("expected reopened item to be pending")
	}

	if got.ActionBy != "" || got.ActionReason != "" {
		t.Fatalf("expected previous action fields to be cleared")
	}
}
