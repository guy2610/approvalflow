package domain

import (
	"encoding/json"
	"testing"
)

func TestPubSubTopics(t *testing.T) {
	if PubSubName != "pubsub" {
		t.Fatalf("unexpected pubsub name: %s", PubSubName)
	}

	tests := map[string]string{
		TopicSubmissionReceived: "submission.received",
		TopicPaymentRequested:   "payment.requested",
		TopicApprovalRequired:   "approval.required",
		TopicAuditEvent:         "audit.event",
	}

	for got, want := range tests {
		if got != want {
			t.Fatalf("expected topic %s, got %s", want, got)
		}
	}
}

func TestSubmissionStatusConstants(t *testing.T) {
	tests := map[SubmissionStatus]string{
		SubmissionAccepted:               "ACCEPTED",
		SubmissionDuplicate:              "DUPLICATE",
		SubmissionAutoApprovedPendingPay: "AUTO_APPROVED_PENDING_PAYMENT",
		SubmissionPaymentPending:         "PAYMENT_PENDING",
		SubmissionPaid:                   "PAID",
		SubmissionPaymentFailed:          "PAYMENT_FAILED",
		SubmissionHumanReviewRequired:    "HUMAN_REVIEW_REQUIRED",
		SubmissionApprovedByHuman:        "APPROVED_BY_HUMAN",
		SubmissionRejectedByHuman:        "REJECTED_BY_HUMAN",
		SubmissionInfoRequested:          "INFO_REQUESTED",
		SubmissionRejected:               "REJECTED",
	}

	for got, want := range tests {
		if string(got) != want {
			t.Fatalf("expected submission status %s, got %s", want, got)
		}
	}
}

func TestPaymentStatusConstants(t *testing.T) {
	if PaymentSucceeded != "SUCCEEDED" {
		t.Fatalf("unexpected payment success status: %s", PaymentSucceeded)
	}

	if PaymentFailed != "FAILED" {
		t.Fatalf("unexpected payment failed status: %s", PaymentFailed)
	}
}

func TestSubmissionReceivedEventRevisionJSON(t *testing.T) {
	event := SubmissionReceivedEvent{TrackingID: "T", EventID: "source", RevisionNumber: 2}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SubmissionReceivedEvent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RevisionNumber != 2 || decoded.TrackingID != "T" {
		t.Fatal(decoded)
	}
}
