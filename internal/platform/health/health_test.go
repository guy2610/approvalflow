package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"approvalflow/internal/platform/httpx"
)

func TestHandlerReturnsHealthResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(httpx.CorrelationIDHeader, "corr-health")

	rec := httptest.NewRecorder()

	handler := httpx.CorrelationMiddleware(Handler("test-service"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body Response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Service != "test-service" {
		t.Fatalf("expected service test-service, got %s", body.Service)
	}

	if body.Status != "ok" {
		t.Fatalf("expected status ok, got %s", body.Status)
	}

	if body.CorrelationID != "corr-health" {
		t.Fatalf("expected correlation id corr-health, got %s", body.CorrelationID)
	}

	if body.TimeUTC == "" {
		t.Fatalf("expected time_utc to be populated")
	}
}
