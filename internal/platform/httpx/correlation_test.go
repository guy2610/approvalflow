package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorrelationMiddlewareUsesExistingHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := CorrelationIDFromContext(r.Context())
		if got != "test-correlation" {
			t.Fatalf("expected existing correlation id, got %q", got)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(CorrelationIDHeader, "test-correlation")
	rr := httptest.NewRecorder()

	CorrelationMiddleware(next).ServeHTTP(rr, req)

	if rr.Header().Get(CorrelationIDHeader) != "test-correlation" {
		t.Fatalf("expected response correlation header")
	}
}

func TestCorrelationMiddlewareCreatesHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := CorrelationIDFromContext(r.Context())
		if got == "" {
			t.Fatalf("expected generated correlation id")
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	CorrelationMiddleware(next).ServeHTTP(rr, req)

	if rr.Header().Get(CorrelationIDHeader) == "" {
		t.Fatalf("expected response correlation header")
	}
}
