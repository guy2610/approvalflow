package main

import "testing"

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
