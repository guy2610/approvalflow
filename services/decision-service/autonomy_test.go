package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"approvalflow/internal/domain"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/logger"
	"approvalflow/internal/policy"
)

// The first two GETs capture their snapshots before the barrier releases either.
// Unconditional writes deliberately model the legacy lost-update behavior.
type budgetStore struct {
	mu                                         sync.Mutex
	raw                                        json.RawMessage
	version, reads, conflicts, writes, strong  int
	createAttempts                             int
	loseCreateResponse                         bool
	reconciliationAbsent, reconciliationErrors int
	createWins, createFailures                 int
	barrier                                    chan struct{}
	failRead, failWrite, alwaysConflict        bool
}

func (f *budgetStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if r.Method == http.MethodGet {
		f.reads++
		if r.URL.Query().Get("consistency") == "strong" {
			f.strong++
		}
		raw, version, n := append([]byte(nil), f.raw...), f.version, f.reads
		transientFailure := false
		if f.createAttempts > 0 {
			if f.reconciliationErrors > 0 {
				f.reconciliationErrors--
				transientFailure = true
			} else if f.reconciliationAbsent > 0 {
				f.reconciliationAbsent--
				raw = nil
			}
		}
		fail := f.failRead || transientFailure
		if f.barrier != nil && n == 2 {
			close(f.barrier)
		}
		f.mu.Unlock()
		if f.barrier != nil && n <= 2 {
			select {
			case <-f.barrier:
			case <-r.Context().Done():
				return
			}
		}
		if fail {
			http.Error(w, "read failed", 500)
			return
		}
		if len(raw) == 0 {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("ETag", fmt.Sprint(version))
		_, _ = w.Write(raw)
		return
	}
	defer f.mu.Unlock()
	type item struct {
		Value   json.RawMessage `json:"value"`
		ETag    string          `json:"etag"`
		Options struct {
			Concurrency string `json:"concurrency"`
		} `json:"options"`
	}
	var value item
	if strings.HasSuffix(r.URL.Path, "/transaction") {
		f.createAttempts++
		var tx struct {
			Operations []struct {
				Request item `json:"request"`
			} `json:"operations"`
		}
		_ = json.NewDecoder(r.Body).Decode(&tx)
		value = tx.Operations[0].Request
	} else {
		var items []item
		_ = json.NewDecoder(r.Body).Decode(&items)
		value = items[0]
	}
	if f.failWrite {
		http.Error(w, "write failed", 500)
		return
	}
	if f.alwaysConflict || (value.Options.Concurrency == "first-write" && ((value.ETag == "" && len(f.raw) > 0) || (value.ETag != "" && value.ETag != fmt.Sprint(f.version)))) {
		f.conflicts++
		if strings.HasSuffix(r.URL.Path, "/transaction") {
			f.createFailures++
			http.Error(w, `{"errorCode":"ERR_STATE_TRANSACTION","message":"transaction failed"}`, 500)
			return
		}
		http.Error(w, "ETag mismatch", 409)
		return
	}
	f.raw = append([]byte(nil), value.Value...)
	f.version++
	f.writes++
	if strings.HasSuffix(r.URL.Path, "/transaction") {
		f.createWins++
		if f.loseCreateResponse {
			http.Error(w, "transaction response lost", 500)
			return
		}
	}
	w.WriteHeader(204)
}
func budgetFixture(t *testing.T, initial float64, concurrent bool) (*server, *budgetStore, policy.Config) {
	t.Helper()
	f := &budgetStore{version: 1}
	f.raw = []byte(fmt.Sprintf(`{"date":"2026-05-30","submitter_totals":{"alice":%v},"vendor_totals":{"vendor":%v}}`, initial, initial))
	if concurrent {
		f.barrier = make(chan struct{})
	}
	h := httptest.NewServer(f)
	t.Cleanup(h.Close)
	t.Setenv("DAPR_HTTP_PORT", strings.TrimPrefix(h.URL, "http://127.0.0.1:"))
	cfg := policy.DefaultConfig()
	cfg.CumulativeAutonomyEnabled = true
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 1000
	cfg.MaxDailyAutoApprovedPerVendorUSD = 1000
	return &server{dapr: daprclient.NewFromEnv(), log: logger.New("test")}, f, cfg
}
func reserveTest(s *server, tracking, event string, revision int, amount float64, cfg policy.Config) domain.DecisionResult {
	d, _, err := s.applyCumulativeAutonomyBudget(context.Background(), domain.SubmissionRequest{Date: "2026-05-30", Submitter: "Alice", Vendor: "Vendor"}, domain.DecisionResult{Route: domain.RouteAutoApprove, AmountUSD: amount}, domain.SubmissionReceivedEvent{TrackingID: tracking, EventID: event, RevisionNumber: revision}, cfg)
	if err != nil {
		panic(err)
	}
	return d
}
func budgetTotals(t *testing.T, f *budgetStore) autonomyDailyExposure {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var e autonomyDailyExposure
	if err := json.Unmarshal(f.raw, &e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestConcurrentAutonomyReservations(t *testing.T) {
	s, f, cfg := budgetFixture(t, 700, true)
	results := make(chan domain.DecisionResult, 2)
	for _, id := range []string{"A", "B"} {
		go func(id string) { results <- reserveTest(s, id, "event-"+id, 1, 200, cfg) }(id)
	}
	admitted := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.Route == domain.RouteAutoApprove {
			admitted++
		} else if result.Route != domain.RouteHumanReview || len(result.Violations) != 2 || result.Violations[0].RuleID != policy.AutonomyBudgetRuleID || result.Violations[1].RuleID != policy.AutonomyBudgetRuleID {
			t.Errorf("expected cumulative-budget human review, got %+v", result)
		}
	}
	e := budgetTotals(t, f)
	if f.conflicts != 1 || f.reads != 3 || f.strong != 3 || f.writes != 2 || len(e.Reservations) != 2 {
		t.Errorf("CAS path: reads=%d strong=%d conflicts=%d writes=%d reservations=%d", f.reads, f.strong, f.conflicts, f.writes, len(e.Reservations))
	}
	if admitted != 1 || e.SubmitterTotals["alice"] != 900 || e.VendorTotals["vendor"] != 900 {
		t.Fatalf("admitted=%d stored=%v/%v actual admitted exposure=%d; want one admission and 900", admitted, e.SubmitterTotals["alice"], e.VendorTotals["vendor"], 700+admitted*200)
	}
}
func TestAutonomyRedelivery(t *testing.T) {
	for _, secondEvent := range []string{"original", "different-source-event"} {
		t.Run(secondEvent, func(t *testing.T) {
			s, f, cfg := budgetFixture(t, 100, false)
			first := reserveTest(s, "T", "original", 1, 50, cfg)
			second := reserveTest(s, "T", secondEvent, 1, 50, cfg)
			e := budgetTotals(t, f)
			if len(e.Reservations) != 1 || e.Reservations["T:revision:1"].SourceEventID != "original" || f.writes != 1 {
				t.Errorf("reservation diagnostics or write count incorrect: %+v writes=%d", e.Reservations, f.writes)
			}
			if first.Route != domain.RouteAutoApprove || second.Route != first.Route || e.SubmitterTotals["alice"] != 150 || e.VendorTotals["vendor"] != 150 {
				t.Fatalf("routes=%s/%s exposure=%v/%v; want repeated admission with exposure 150", first.Route, second.Route, e.SubmitterTotals["alice"], e.VendorTotals["vendor"])
			}
		})
	}
}

func TestDeniedReservationRemainsAuthoritative(t *testing.T) {
	s, f, cfg := budgetFixture(t, 990, false)
	first := reserveTest(s, "T", "original", 1, 50, cfg)
	if first.Route != domain.RouteHumanReview {
		t.Fatal(first)
	}
	e := budgetTotals(t, f)
	if e.SubmitterTotals["alice"] != 990 || e.VendorTotals["vendor"] != 990 || e.Reservations["T:revision:1"].Admitted {
		t.Fatal(e)
	}
	e.SubmitterTotals["alice"] = 0
	e.VendorTotals["vendor"] = 0
	f.raw, _ = json.Marshal(e)
	f.version++
	cfg.CumulativeAutonomyEnabled = false
	second := reserveTest(s, "T", "new-source", 1, 50, cfg)
	if !reflect.DeepEqual(first, second) || f.writes != 1 {
		t.Fatalf("stored denial changed: %+v / %+v", first, second)
	}
	cfg.CumulativeAutonomyEnabled = true
	third := reserveTest(s, "T", "revision-two", 2, 50, cfg)
	if third.Route != domain.RouteAutoApprove || len(budgetTotals(t, f).Reservations) != 2 {
		t.Fatal("new revision must use current config/exposure")
	}
}

func TestAdmittedReservationSurvivesPolicyChange(t *testing.T) {
	s, f, cfg := budgetFixture(t, 100, false)
	first := reserveTest(s, "T", "original", 1, 50, cfg)
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 1
	second := reserveTest(s, "T", "changed", 1, 50, cfg)
	if !reflect.DeepEqual(first, second) || f.writes != 1 {
		t.Fatal("admitted outcome was reevaluated")
	}
	if reserveTest(s, "T", "new-revision", 2, 50, cfg).Route != domain.RouteHumanReview {
		t.Fatal("new revision ignored new policy")
	}
}

func TestConcurrentFirstAutonomyReservations(t *testing.T) {
	s, f, cfg := budgetFixture(t, 0, true)
	f.raw = nil
	f.version = 0
	cfg.MaxDailyAutoApprovedPerSubmitterUSD = 250
	cfg.MaxDailyAutoApprovedPerVendorUSD = 250
	results := make(chan domain.DecisionResult, 2)
	for _, id := range []string{"A", "B"} {
		go func(id string) { results <- reserveTest(s, id, id, 1, 200, cfg) }(id)
	}
	admitted := 0
	for i := 0; i < 2; i++ {
		if (<-results).Route == domain.RouteAutoApprove {
			admitted++
		}
	}
	e := budgetTotals(t, f)
	storedAdmitted := 0
	for _, id := range []string{"A", "B"} {
		saved, ok := e.Reservations[id+":revision:1"]
		if !ok || saved.TrackingID != id || saved.RevisionNumber != 1 || saved.ReservationID != id+":revision:1" || saved.SourceEventID != id || saved.AmountUSD != 200 {
			t.Errorf("incorrect reservation: %+v", saved)
		}
		if saved.Admitted {
			storedAdmitted++
		} else if len(saved.Violations) != 2 {
			t.Errorf("denial missing budget violations: %+v", saved)
		}
	}
	if storedAdmitted != 1 {
		t.Errorf("stored admissions=%d", storedAdmitted)
	}
	if f.createWins != 1 || f.createFailures != 1 || f.writes != 2 || admitted != 1 || f.conflicts != 1 || f.reads != 3 || e.SubmitterTotals["alice"] != 200 || e.VendorTotals["vendor"] != 200 || len(e.Reservations) != 2 {
		t.Fatalf("incorrect create conflict outcome: %+v admitted=%d conflicts=%d reads=%d", e, admitted, f.conflicts, f.reads)
	}
}

func TestAutonomyFailuresDoNotWriteDecisionsOrPublish(t *testing.T) {
	for _, mode := range []string{"read", "write", "exhaustion", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, f, _ := budgetFixture(t, 100, false)
			t.Setenv("POLICY_CONFIG_PATH", "../../data/policy-config.json")
			f.failRead = mode == "read"
			f.failWrite = mode == "write"
			f.alwaysConflict = mode == "exhaustion"
			mutations := 0
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1.0/state/") {
					if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/transaction") {
						f.ServeHTTP(w, r)
						return
					}
					var items []struct {
						Key string `json:"key"`
					}
					raw := new(bytes.Buffer)
					_, _ = raw.ReadFrom(r.Body)
					_ = json.Unmarshal(raw.Bytes(), &items)
					r.Body = http.NoBody
					if len(items) > 0 && strings.HasPrefix(items[0].Key, "autonomy:daily:") {
						r.Body = io.NopCloser(bytes.NewReader(raw.Bytes()))
						f.ServeHTTP(w, r)
						return
					}
					mutations++
					w.WriteHeader(204)
					return
				}
				if strings.Contains(r.URL.Path, "agent-service") {
					http.Error(w, "unavailable", 503)
					return
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(domain.SubmissionRecord{RevisionNumber: 1, Request: domain.SubmissionRequest{Date: "2026-05-30", Submitter: "Alice", Vendor: "Vendor", Total: 50, Currency: "USD", Category: "travel", VendorKnown: true, ReceiptPresent: true, LineItems: []domain.LineItem{{Description: "Taxi", Quantity: 1, UnitPrice: 50}}}})
					return
				}
				mutations++
				w.WriteHeader(204)
			}))
			defer proxy.Close()
			t.Setenv("DAPR_HTTP_PORT", strings.TrimPrefix(proxy.URL, "http://127.0.0.1:"))
			s.dapr = daprclient.NewFromEnv()
			req := httptest.NewRequest(http.MethodPost, "/events/submission-received", strings.NewReader(`{"data":{"tracking_id":"T","event_id":"source","revision_number":1}}`))
			if mode == "cancel" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			s.handleSubmissionReceived(w, req)
			if w.Code != 500 || mutations != 0 || f.writes != 0 {
				t.Fatalf("status=%d downstream mutations=%d aggregate writes=%d", w.Code, mutations, f.writes)
			}
			if mode == "exhaustion" && f.conflicts != autonomyReservationAttempts {
				t.Fatalf("conflicts=%d", f.conflicts)
			}
		})
	}
}

func TestConcurrentSameRevisionReservation(t *testing.T) {
	s, f, cfg := budgetFixture(t, 100, true)
	results := make(chan domain.DecisionResult, 2)
	for _, source := range []string{"source-a", "source-b"} {
		go func(source string) { results <- reserveTest(s, "T", source, 1, 50, cfg) }(source)
	}
	first, second := <-results, <-results
	e := budgetTotals(t, f)
	saved := e.Reservations["T:revision:1"]
	if !reflect.DeepEqual(first, second) || first.Route != domain.RouteAutoApprove || len(e.Reservations) != 1 || e.SubmitterTotals["alice"] != 150 || e.VendorTotals["vendor"] != 150 || f.conflicts != 1 || f.writes != 1 || f.reads != 3 || (saved.SourceEventID != "source-a" && saved.SourceEventID != "source-b") {
		t.Fatalf("incorrect concurrent redelivery: %+v first=%+v second=%+v", e, first, second)
	}
}

func TestLegacyAutonomyStatePreservesUnrelatedTotals(t *testing.T) {
	s, f, cfg := budgetFixture(t, 100, false)
	f.raw = []byte(`{"date":"2026-05-30","submitter_totals":{"alice":100,"bob":321},"vendor_totals":{"vendor":100,"other":654}}`)
	reserveTest(s, "T", "original", 1, 50, cfg)
	e := budgetTotals(t, f)
	if e.SubmitterTotals["alice"] != 150 || e.SubmitterTotals["bob"] != 321 || e.VendorTotals["vendor"] != 150 || e.VendorTotals["other"] != 654 || len(e.Reservations) != 1 {
		t.Fatalf("legacy totals changed: %+v", e)
	}
}

// Handler fixture records all downstream activity separately from autonomy state.
type revisionFixture struct {
	mu               sync.Mutex
	record           domain.SubmissionRecord
	calls, mutations int
	failDecision     bool
	budget           *budgetStore
}

func (f *revisionFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.0/state/") {
		f.budget.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1.0/state/") {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if strings.Contains(string(raw), `"key":"autonomy:daily:`) {
			f.budget.ServeHTTP(w, r)
			return
		}
		if f.failDecision {
			f.failDecision = false
			http.Error(w, "decision save failed", 500)
			return
		}
	}
	if strings.Contains(r.URL.Path, "agent-service") {
		http.Error(w, "agent unavailable", 503)
		return
	}
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(f.record)
		return
	}
	f.mutations++
	w.WriteHeader(204)
}
func newRevisionFixture(t *testing.T) (*server, *revisionFixture) {
	s, b, _ := budgetFixture(t, 100, false)
	f := &revisionFixture{budget: b, record: domain.SubmissionRecord{TrackingID: "T", RevisionNumber: 1, Request: domain.SubmissionRequest{Date: "2026-05-30", Submitter: "Alice", Vendor: "Vendor", Total: 50, Currency: "USD", Category: "travel", VendorKnown: true, ReceiptPresent: true, LineItems: []domain.LineItem{{Description: "Taxi", Quantity: 1, UnitPrice: 50}}}}}
	h := httptest.NewServer(f)
	t.Cleanup(h.Close)
	t.Setenv("POLICY_CONFIG_PATH", "../../data/policy-config.json")
	t.Setenv("DAPR_HTTP_PORT", strings.TrimPrefix(h.URL, "http://127.0.0.1:"))
	s.dapr = daprclient.NewFromEnv()
	return s, f
}
func deliverRevision(s *server, tracking, source string, revision int) int {
	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"tracking_id": tracking, "event_id": source, "revision_number": revision}})
	w := httptest.NewRecorder()
	s.handleSubmissionReceived(w, httptest.NewRequest(http.MethodPost, "/events/submission-received", bytes.NewReader(raw)))
	return w.Code
}
func TestDelayedRevisionRedelivery(t *testing.T) {
	s, f := newRevisionFixture(t)
	if code := deliverRevision(s, "T", "original", 1); code != 204 {
		t.Fatal(code)
	}
	before := budgetTotals(t, f.budget)
	f.mu.Lock()
	f.record.RevisionNumber = 2
	f.record.Request.Total = 75
	f.record.Request.LineItems[0].UnitPrice = 75
	calls, mutations := f.calls, f.mutations
	f.mu.Unlock()
	if code := deliverRevision(s, "T", "original", 1); code != 204 {
		t.Fatal(code)
	}
	after := budgetTotals(t, f.budget)
	f.mu.Lock()
	staleCalls, staleMutations := f.calls-calls, f.mutations-mutations
	f.mu.Unlock()
	if !reflect.DeepEqual(before, after) || staleCalls != 1 || staleMutations != 0 {
		t.Fatalf("stale event evaluated or mutated newer workflow: calls=%d mutations=%d before=%+v after=%+v", staleCalls, staleMutations, before, after)
	}
	if code := deliverRevision(s, "T", "revision-two", 2); code != 204 {
		t.Fatal(code)
	}
	if code := deliverRevision(s, "T", "different-source", 2); code != 204 {
		t.Fatal(code)
	}
	e := budgetTotals(t, f.budget)
	if len(e.Reservations) != 2 || e.SubmitterTotals["alice"] != 225 || e.VendorTotals["vendor"] != 225 || e.Reservations["T:revision:1"].AmountUSD != 50 || e.Reservations["T:revision:2"].AmountUSD != 75 || e.Reservations["T:revision:2"].SourceEventID != "revision-two" {
		t.Fatal(e)
	}
}
func TestInvalidAndFutureRevisionEvents(t *testing.T) {
	for _, tt := range []struct {
		name, tracking string
		revision       int
	}{{"zero", "T", 0}, {"negative", "T", -1}, {"empty", "", 1}, {"future", "T", 2}} {
		t.Run(tt.name, func(t *testing.T) {
			s, f := newRevisionFixture(t)
			before := budgetTotals(t, f.budget)
			code := deliverRevision(s, tt.tracking, "source", tt.revision)
			after := budgetTotals(t, f.budget)
			f.mu.Lock()
			calls, mutations := f.calls, f.mutations
			f.mu.Unlock()
			wantCalls := 0
			if tt.name == "future" {
				wantCalls = 1
			}
			if code < 400 || calls != wantCalls || mutations != 0 || !reflect.DeepEqual(before, after) {
				t.Fatalf("code=%d calls=%d mutations=%d", code, calls, mutations)
			}
		})
	}
}
func TestReservationSurvivesLaterDecisionFailure(t *testing.T) {
	s, f := newRevisionFixture(t)
	f.failDecision = true
	if code := deliverRevision(s, "T", "source", 1); code != 500 {
		t.Fatal(code)
	}
	before := budgetTotals(t, f.budget)
	if before.SubmitterTotals["alice"] != 150 || len(before.Reservations) != 1 {
		t.Fatal(before)
	}
	if code := deliverRevision(s, "T", "source", 1); code != 204 {
		t.Fatal(code)
	}
	if after := budgetTotals(t, f.budget); !reflect.DeepEqual(before, after) {
		t.Fatal("redelivery incremented exposure")
	}
}

func TestAmbiguousAutonomyCreationReconciliation(t *testing.T) {
	for _, mode := range []string{"committed-response-lost", "transient-reads", "still-absent", "reads-fail"} {
		t.Run(mode, func(t *testing.T) {
			s, f, cfg := budgetFixture(t, 0, false)
			f.raw = nil
			switch mode {
			case "committed-response-lost":
				f.loseCreateResponse = true
			case "transient-reads":
				f.loseCreateResponse = true
				f.reconciliationErrors = 1
				f.reconciliationAbsent = 1
			case "still-absent":
				f.failWrite = true
			case "reads-fail":
				f.failWrite = true
				f.reconciliationErrors = autonomyCreationReadAttempts
			}
			d, _, err := s.applyCumulativeAutonomyBudget(context.Background(), domain.SubmissionRequest{Date: "2026-05-30", Submitter: "Alice", Vendor: "Vendor"}, domain.DecisionResult{Route: domain.RouteAutoApprove, AmountUSD: 50}, domain.SubmissionReceivedEvent{TrackingID: "T", EventID: "original", RevisionNumber: 1}, cfg)
			if mode == "still-absent" || mode == "reads-fail" {
				if err == nil || errors.Is(err, daprclient.ErrStateConflict) || f.writes != 0 || f.reads != 1+autonomyCreationReadAttempts || f.createAttempts != 1 {
					t.Fatalf("must fail closed within bound: err=%v writes=%d reads=%d creates=%d", err, f.writes, f.reads, f.createAttempts)
				}
				return
			}
			e := budgetTotals(t, f)
			if err != nil || d.Route != domain.RouteAutoApprove || f.createAttempts != 1 || f.writes != 1 || len(e.Reservations) != 1 || e.SubmitterTotals["alice"] != 50 || e.VendorTotals["vendor"] != 50 {
				t.Fatalf("lost response not reconciled: err=%v state=%+v writes=%d", err, e, f.writes)
			}
		})
	}
}
