package dapr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)

	return &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}, server
}

func TestInvokeJSONUsesGetWhenPayloadIsNil(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}

		if r.URL.Path != "/v1.0/invoke/submission-service/method/internal/submissions/sub_123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	defer server.Close()

	var out map[string]string
	status, err := client.InvokeJSON(context.Background(), "submission-service", "/internal/submissions/sub_123", nil, &out)
	if err != nil {
		t.Fatalf("InvokeJSON returned error: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	if out["status"] != "ok" {
		t.Fatalf("expected decoded response status ok, got %s", out["status"])
	}
}

func TestInvokeJSONUsesPostWhenPayloadIsProvided(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		if body["hello"] != "world" {
			t.Fatalf("unexpected request body: %v", body)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	status, err := client.InvokeJSON(context.Background(), "app", "method", map[string]string{"hello": "world"}, nil)
	if err != nil {
		t.Fatalf("InvokeJSON returned error: %v", err)
	}

	if status != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", status)
	}
}

func TestInvokeRawReturnsErrorForDownstreamError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "downstream failed", http.StatusBadGateway)
	})
	defer server.Close()

	status, raw, err := client.InvokeRaw(context.Background(), "app", "method", http.MethodGet, nil)
	if err == nil {
		t.Fatalf("expected error")
	}

	if status != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", status)
	}

	if !strings.Contains(string(raw), "downstream failed") {
		t.Fatalf("expected raw body to include downstream error, got %s", string(raw))
	}
}

func TestGetStateHandlesNotFound(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	var out map[string]string
	found, err := client.GetState(context.Background(), "statestore", "missing", &out)
	if err != nil {
		t.Fatalf("GetState returned error: %v", err)
	}

	if found {
		t.Fatalf("expected state not found")
	}
}

func TestGetStateDecodesValue(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	defer server.Close()

	var out map[string]string
	found, err := client.GetState(context.Background(), "statestore", "key", &out)
	if err != nil {
		t.Fatalf("GetState returned error: %v", err)
	}

	if !found {
		t.Fatalf("expected state found")
	}

	if out["status"] != "ok" {
		t.Fatalf("expected decoded status ok, got %s", out["status"])
	}
}

func TestGetSecretDecodesSecretValue(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"payment-provider-token": "secret-value"})
	})
	defer server.Close()

	value, found, err := client.GetSecret(context.Background(), "localsecrets", "payment-provider-token")
	if err != nil {
		t.Fatalf("GetSecret returned error: %v", err)
	}

	if !found {
		t.Fatalf("expected secret found")
	}

	if value != "secret-value" {
		t.Fatalf("expected secret-value, got %s", value)
	}
}

func TestInvokeRawPassthroughPreservesBusinessError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "approval no longer pending", http.StatusConflict)
	})
	defer server.Close()

	status, raw, err := client.InvokeRawPassthrough(context.Background(), "approval-service", "approvals/sub_1/approve", http.MethodPost, []byte(`{}`))
	if err != nil {
		t.Fatalf("InvokeRawPassthrough returned transport error: %v", err)
	}

	if status != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", status)
	}

	if !strings.Contains(string(raw), "approval no longer pending") {
		t.Fatalf("expected downstream body to be preserved, got %s", string(raw))
	}
}
