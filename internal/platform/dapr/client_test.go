package dapr

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGetStateStrongRequestsStrongConsistency(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1.0/state/statestore/duplicate:fingerprint" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("consistency"); got != "strong" {
			t.Fatalf("expected strong consistency, got %q", got)
		}
		_ = json.NewEncoder(w).Encode("sub_123")
	})
	defer server.Close()

	var trackingID string
	found, err := client.GetStateStrong(
		context.Background(),
		"statestore",
		"duplicate:fingerprint",
		&trackingID,
	)
	if err != nil {
		t.Fatalf("GetStateStrong returned error: %v", err)
	}
	if !found || trackingID != "sub_123" {
		t.Fatalf("expected sub_123, got found=%v value=%q", found, trackingID)
	}
}

func TestSaveStateTransactionUsesCreateOnlyAndAtomicUpserts(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1.0/state/statestore/transaction" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var request stateTransactionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode transaction request: %v", err)
		}
		if len(request.Operations) != 2 {
			t.Fatalf("expected two operations, got %d", len(request.Operations))
		}

		claim := request.Operations[0]
		if claim.Operation != "upsert" || claim.Request.Key != "duplicate:fingerprint" {
			t.Fatalf("unexpected claim operation: %+v", claim)
		}
		if claim.Request.Options == nil || claim.Request.Options.Concurrency != "first-write" || claim.Request.Options.Consistency != "strong" {
			t.Fatalf("unexpected claim options: %+v", claim.Request.Options)
		}
		var claimValue string
		claimRaw, _ := json.Marshal(claim.Request.Value)
		if err := json.Unmarshal(claimRaw, &claimValue); err != nil || claimValue != "sub_123" {
			t.Fatalf("unexpected claim value: %v", claim.Request.Value)
		}

		record := request.Operations[1]
		if record.Operation != "upsert" || record.Request.Key != "submission:sub_123" {
			t.Fatalf("unexpected record operation: %+v", record)
		}
		if record.Request.Options == nil || record.Request.Options.Concurrency != "" || record.Request.Options.Consistency != "strong" {
			t.Fatalf("unexpected record options: %+v", record.Request.Options)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	err := client.SaveStateTransaction(context.Background(), "statestore", []StateTransactionItem{
		{Key: "duplicate:fingerprint", Value: "sub_123", CreateOnly: true},
		{Key: "submission:sub_123", Value: map[string]string{"status": "ACCEPTED"}},
	})
	if err != nil {
		t.Fatalf("SaveStateTransaction returned error: %v", err)
	}
}

func TestSaveStateTransactionReturnsSidecarError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "transaction conflict", http.StatusInternalServerError)
	})
	defer server.Close()

	err := client.SaveStateTransaction(context.Background(), "statestore", []StateTransactionItem{
		{Key: "duplicate:fingerprint", Value: "sub_123", CreateOnly: true},
	})
	if err == nil || !strings.Contains(err.Error(), "transaction conflict") {
		t.Fatalf("expected sidecar transaction error, got %v", err)
	}
}

func TestSaveStateTransactionRejectsInvalidItemsWithoutRequest(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("HTTP request must not be sent for invalid transaction")
	})
	defer server.Close()

	if err := client.SaveStateTransaction(context.Background(), "statestore", nil); err == nil {
		t.Fatalf("expected empty transaction error")
	}
	if err := client.SaveStateTransaction(context.Background(), "statestore", []StateTransactionItem{{Value: "value"}}); err == nil {
		t.Fatalf("expected empty key error")
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

func TestGetStateWithETagReturnsDecodedValueAndETag(t *testing.T) {
	client, server := newTestClient(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}

		w.Header().Set("ETag", `"42"`)
		_ = json.NewEncoder(w).Encode(
			map[string]string{"status": "PENDING"},
		)
	})
	defer server.Close()

	var out map[string]string
	found, etag, err := client.GetStateWithETag(
		context.Background(),
		"statestore",
		"approval:sub_123",
		&out,
	)
	if err != nil {
		t.Fatalf("GetStateWithETag returned error: %v", err)
	}

	if !found {
		t.Fatalf("expected state to be found")
	}

	if etag != "42" {
		t.Fatalf("expected ETag 42, got %q", etag)
	}

	if out["status"] != "PENDING" {
		t.Fatalf("unexpected decoded state: %v", out)
	}
}

func TestGetStateWithETagRejectsMissingETag(t *testing.T) {
	client, server := newTestClient(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_ = json.NewEncoder(w).Encode(
			map[string]string{"status": "PENDING"},
		)
	})
	defer server.Close()

	var out map[string]string
	found, etag, err := client.GetStateWithETag(
		context.Background(),
		"statestore",
		"approval:sub_123",
		&out,
	)

	if err == nil {
		t.Fatalf("expected missing ETag error")
	}

	if found {
		t.Fatalf("state must not be reported as safely loaded")
	}

	if etag != "" {
		t.Fatalf("expected empty ETag, got %q", etag)
	}
}

func TestSaveStateWithETagUsesFirstWriteConcurrency(t *testing.T) {
	client, server := newTestClient(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		var items []struct {
			Key     string            `json:"key"`
			Value   map[string]string `json:"value"`
			ETag    string            `json:"etag"`
			Options stateOptions      `json:"options"`
		}

		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			t.Fatalf("decode state request: %v", err)
		}

		if len(items) != 1 {
			t.Fatalf("expected one state item, got %d", len(items))
		}

		if items[0].Key != "approval:sub_123" {
			t.Fatalf("unexpected state key: %s", items[0].Key)
		}

		if items[0].ETag != "42" {
			t.Fatalf("expected ETag 42, got %q", items[0].ETag)
		}

		if items[0].Options.Concurrency != "first-write" {
			t.Fatalf(
				"expected first-write concurrency, got %q",
				items[0].Options.Concurrency,
			)
		}

		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	err := client.SaveStateWithETag(
		context.Background(),
		"statestore",
		"approval:sub_123",
		map[string]string{"status": "APPROVED"},
		"42",
	)
	if err != nil {
		t.Fatalf("SaveStateWithETag returned error: %v", err)
	}
}

func TestSaveStateWithETagMapsConflict(t *testing.T) {
	client, server := newTestClient(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.Error(
			w,
			"ETag mismatch",
			http.StatusConflict,
		)
	})
	defer server.Close()

	err := client.SaveStateWithETag(
		context.Background(),
		"statestore",
		"approval:sub_123",
		map[string]string{"status": "APPROVED"},
		"stale-etag",
	)

	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf(
			"expected ErrStateConflict, got %v",
			err,
		)
	}
}

func TestSaveStateWithETagRejectsEmptyETag(t *testing.T) {
	client, server := newTestClient(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		t.Fatalf("HTTP request must not be sent for empty ETag")
	})
	defer server.Close()

	err := client.SaveStateWithETag(
		context.Background(),
		"statestore",
		"approval:sub_123",
		map[string]string{"status": "APPROVED"},
		"",
	)

	if err == nil {
		t.Fatalf("expected empty ETag error")
	}
}

func TestGetStateStrongWithETag(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("consistency") != "strong" {
			t.Error("missing strong consistency")
		}
		w.Header().Set("ETag", `"7"`)
		_, _ = w.Write([]byte(`{"total":100}`))
	})
	defer server.Close()
	var out map[string]int
	found, etag, err := client.GetStateStrongWithETag(context.Background(), "statestore", "autonomy:daily:2026-05-30", &out)
	if err != nil || !found || etag != "7" || out["total"] != 100 {
		t.Fatalf("found=%v etag=%s state=%v err=%v", found, etag, out, err)
	}
}

func TestTransaction500RemainsInfrastructureError(t *testing.T) {
	client, server := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errorCode":"ERR_STATE_TRANSACTION","message":"transaction failed"}`, 500)
	})
	defer server.Close()
	err := client.SaveStateTransaction(context.Background(), "statestore", []StateTransactionItem{{Key: "autonomy:daily:2026-05-30", Value: 1, CreateOnly: true}})
	if err == nil || errors.Is(err, ErrStateConflict) {
		t.Fatalf("transaction failure must remain ambiguous: %v", err)
	}
}
