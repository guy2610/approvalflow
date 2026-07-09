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
