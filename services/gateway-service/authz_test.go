package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireDemoRoleAllowsAllowedRole(t *testing.T) {
	handler := requireDemoRole(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "approver", "admin")

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	req.Header.Set(demoRoleHeader, "approver")

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRequireDemoRoleRejectsMissingRole(t *testing.T) {
	handler := requireDemoRole(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "approver", "admin")

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestRequireDemoRoleRejectsWrongRole(t *testing.T) {
	handler := requireDemoRole(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "approver", "admin")

	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	req.Header.Set(demoRoleHeader, "viewer")

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestRequireDemoRoleAllowsCaseInsensitiveRole(t *testing.T) {
	handler := requireDemoRole(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "auditor", "admin")

	req := httptest.NewRequest(http.MethodGet, "/audit/test", nil)
	req.Header.Set(demoRoleHeader, " AUDITOR ")

	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}
