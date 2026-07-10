package workflow

import (
	"testing"

	"approvalflow/internal/domain"
)

func TestCanTransitionAllowsValidWorkflowTransitions(t *testing.T) {
	tests := []struct {
		name string
		from domain.SubmissionStatus
		to   domain.SubmissionStatus
	}{
		{
			name: "accepted to processing",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionProcessing,
		},
		{
			name: "accepted directly to auto approved",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionAutoApprovedPendingPay,
		},
		{
			name: "accepted directly to human review",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionHumanReviewRequired,
		},
		{
			name: "accepted directly to rejected",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionRejected,
		},
		{
			name: "processing to auto approved",
			from: domain.SubmissionProcessing,
			to:   domain.SubmissionAutoApprovedPendingPay,
		},
		{
			name: "processing to human review",
			from: domain.SubmissionProcessing,
			to:   domain.SubmissionHumanReviewRequired,
		},
		{
			name: "processing to rejected",
			from: domain.SubmissionProcessing,
			to:   domain.SubmissionRejected,
		},
		{
			name: "human review to approved",
			from: domain.SubmissionHumanReviewRequired,
			to:   domain.SubmissionApprovedByHuman,
		},
		{
			name: "human review to rejected",
			from: domain.SubmissionHumanReviewRequired,
			to:   domain.SubmissionRejectedByHuman,
		},
		{
			name: "human review to info requested",
			from: domain.SubmissionHumanReviewRequired,
			to:   domain.SubmissionInfoRequested,
		},
		{
			name: "info requested resumes processing",
			from: domain.SubmissionInfoRequested,
			to:   domain.SubmissionProcessing,
		},
		{
			name: "auto approved to payment pending",
			from: domain.SubmissionAutoApprovedPendingPay,
			to:   domain.SubmissionPaymentPending,
		},
		{
			name: "auto approved directly to paid",
			from: domain.SubmissionAutoApprovedPendingPay,
			to:   domain.SubmissionPaid,
		},
		{
			name: "auto approved directly to payment failed",
			from: domain.SubmissionAutoApprovedPendingPay,
			to:   domain.SubmissionPaymentFailed,
		},
		{
			name: "human approved directly to paid",
			from: domain.SubmissionApprovedByHuman,
			to:   domain.SubmissionPaid,
		},
		{
			name: "payment pending to paid",
			from: domain.SubmissionPaymentPending,
			to:   domain.SubmissionPaid,
		},
		{
			name: "payment pending to failed",
			from: domain.SubmissionPaymentPending,
			to:   domain.SubmissionPaymentFailed,
		},
		{
			name: "same status is idempotent",
			from: domain.SubmissionPaid,
			to:   domain.SubmissionPaid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !CanTransition(tt.from, tt.to) {
				t.Fatalf("expected transition %s -> %s to be allowed", tt.from, tt.to)
			}

			if err := ValidateTransition(tt.from, tt.to); err != nil {
				t.Fatalf("expected valid transition, got error: %v", err)
			}
		})
	}
}

func TestCanTransitionRejectsInvalidWorkflowTransitions(t *testing.T) {
	tests := []struct {
		name string
		from domain.SubmissionStatus
		to   domain.SubmissionStatus
	}{
		{
			name: "accepted cannot become paid",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionPaid,
		},
		{
			name: "processing cannot become approved by human",
			from: domain.SubmissionProcessing,
			to:   domain.SubmissionApprovedByHuman,
		},
		{
			name: "human review cannot become paid",
			from: domain.SubmissionHumanReviewRequired,
			to:   domain.SubmissionPaid,
		},
		{
			name: "info requested cannot become paid",
			from: domain.SubmissionInfoRequested,
			to:   domain.SubmissionPaid,
		},
		{
			name: "paid cannot return to processing",
			from: domain.SubmissionPaid,
			to:   domain.SubmissionProcessing,
		},
		{
			name: "payment failed cannot become paid",
			from: domain.SubmissionPaymentFailed,
			to:   domain.SubmissionPaid,
		},
		{
			name: "rejected cannot become auto approved",
			from: domain.SubmissionRejected,
			to:   domain.SubmissionAutoApprovedPendingPay,
		},
		{
			name: "human rejected cannot become human review",
			from: domain.SubmissionRejectedByHuman,
			to:   domain.SubmissionHumanReviewRequired,
		},
		{
			name: "unknown current status fails closed",
			from: domain.SubmissionStatus("UNKNOWN"),
			to:   domain.SubmissionPaid,
		},
		{
			name: "unknown target status fails closed",
			from: domain.SubmissionAccepted,
			to:   domain.SubmissionStatus("UNKNOWN"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if CanTransition(tt.from, tt.to) {
				t.Fatalf("expected transition %s -> %s to be rejected", tt.from, tt.to)
			}

			if err := ValidateTransition(tt.from, tt.to); err == nil {
				t.Fatalf("expected invalid transition error")
			}
		})
	}
}
