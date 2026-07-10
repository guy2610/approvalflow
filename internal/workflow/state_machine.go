package workflow

import (
	"fmt"

	"approvalflow/internal/domain"
)

// CanTransition reports whether a submission may move from one status to
// another. Re-applying the same status is allowed so retried commands and
// redelivered events remain idempotent.
func CanTransition(from, to domain.SubmissionStatus) bool {
	if from == to {
		return true
	}

	switch from {
	case domain.SubmissionAccepted:
		return isDecisionOutcome(to)

	case domain.SubmissionProcessing:
		return isDecisionOutcome(to)

	case domain.SubmissionHumanReviewRequired:
		switch to {
		case domain.SubmissionApprovedByHuman,
			domain.SubmissionRejectedByHuman,
			domain.SubmissionInfoRequested:
			return true
		default:
			return false
		}

	case domain.SubmissionInfoRequested:
		return to == domain.SubmissionProcessing

	case domain.SubmissionAutoApprovedPendingPay,
		domain.SubmissionApprovedByHuman:
		switch to {
		case domain.SubmissionPaymentPending,
			domain.SubmissionPaid,
			domain.SubmissionPaymentFailed:
			return true
		default:
			return false
		}

	case domain.SubmissionPaymentPending:
		switch to {
		case domain.SubmissionPaid,
			domain.SubmissionPaymentFailed:
			return true
		default:
			return false
		}

	case domain.SubmissionDuplicate,
		domain.SubmissionPaid,
		domain.SubmissionPaymentFailed,
		domain.SubmissionRejected,
		domain.SubmissionRejectedByHuman:
		return false

	default:
		return false
	}
}

// ValidateTransition returns a descriptive error when a requested transition
// violates the submission workflow.
func ValidateTransition(from, to domain.SubmissionStatus) error {
	if CanTransition(from, to) {
		return nil
	}

	return fmt.Errorf("invalid submission status transition: %s -> %s", from, to)
}

func isDecisionOutcome(status domain.SubmissionStatus) bool {
	switch status {
	case domain.SubmissionProcessing,
		domain.SubmissionAutoApprovedPendingPay,
		domain.SubmissionHumanReviewRequired,
		domain.SubmissionRejected:
		return true
	default:
		return false
	}
}
