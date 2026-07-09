package main

import (
	"testing"

	"approvalflow/internal/domain"
)

func TestIsPaymentEligibleStatus(t *testing.T) {
	tests := []struct {
		name   string
		status domain.SubmissionStatus
		want   bool
	}{
		{
			name:   "auto approved pending payment",
			status: domain.SubmissionAutoApprovedPendingPay,
			want:   true,
		},
		{
			name:   "approved by human",
			status: domain.SubmissionApprovedByHuman,
			want:   true,
		},
		{
			name:   "payment pending",
			status: domain.SubmissionPaymentPending,
			want:   true,
		},
		{
			name:   "accepted is not eligible",
			status: domain.SubmissionAccepted,
			want:   false,
		},
		{
			name:   "human review required is not eligible",
			status: domain.SubmissionHumanReviewRequired,
			want:   false,
		},
		{
			name:   "paid is not eligible",
			status: domain.SubmissionPaid,
			want:   false,
		},
		{
			name:   "rejected is not eligible",
			status: domain.SubmissionRejected,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPaymentEligibleStatus(tt.status); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestSubmissionStatusFromPayment(t *testing.T) {
	tests := []struct {
		name   string
		status domain.PaymentStatus
		want   domain.SubmissionStatus
	}{
		{
			name:   "payment succeeded",
			status: domain.PaymentSucceeded,
			want:   domain.SubmissionPaid,
		},
		{
			name:   "payment failed",
			status: domain.PaymentFailed,
			want:   domain.SubmissionPaymentFailed,
		},
		{
			name:   "unknown payment status falls back to payment pending",
			status: domain.PaymentStatus("unexpected"),
			want:   domain.SubmissionPaymentPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := submissionStatusFromPayment(tt.status); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestShouldSimulatePaymentFailureOnlyForCompensationFixture(t *testing.T) {
	if !shouldSimulatePaymentFailure(domain.PaymentRequestedEvent{InvoiceID: "INV-1012"}) {
		t.Fatalf("expected INV-1012 to simulate payment failure")
	}

	if shouldSimulatePaymentFailure(domain.PaymentRequestedEvent{InvoiceID: "INV-1001"}) {
		t.Fatalf("expected INV-1001 to simulate payment success")
	}
}

func TestSimulatePaymentBuildsIdempotentRecord(t *testing.T) {
	srv := &server{}

	event := domain.PaymentRequestedEvent{
		CorrelationID: "corr-123",
		TrackingID:    "sub_123",
		InvoiceID:     "INV-1001",
		AmountUSD:     42,
	}

	payment := srv.simulatePayment(event, "payment:sub_123")

	if payment.PaymentID != "pay_sub_123" {
		t.Fatalf("unexpected payment id: %s", payment.PaymentID)
	}

	if payment.Status != domain.PaymentSucceeded {
		t.Fatalf("expected payment succeeded, got %s", payment.Status)
	}

	if payment.IdempotencyKey != "payment:sub_123" {
		t.Fatalf("unexpected idempotency key: %s", payment.IdempotencyKey)
	}

	if payment.CorrelationID != "corr-123" {
		t.Fatalf("unexpected correlation id: %s", payment.CorrelationID)
	}

	if payment.CreatedAtUTC.IsZero() || payment.UpdatedAtUTC.IsZero() {
		t.Fatalf("expected timestamps to be populated")
	}
}

func TestDuplicatePaymentReason(t *testing.T) {
	if got := duplicatePaymentReason(domain.PaymentSucceeded); got == "" {
		t.Fatalf("expected duplicate success reason")
	}

	if got := duplicatePaymentReason(domain.PaymentFailed); got == "" {
		t.Fatalf("expected duplicate failure reason")
	}
}
