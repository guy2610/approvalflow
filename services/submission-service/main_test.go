package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"approvalflow/internal/domain"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/logger"
)

type fakeSubmissionState struct {
	mu                    sync.Mutex
	values                map[string]json.RawMessage
	legacyDuplicateReads  int
	legacyReadsReleased   chan struct{}
	receivedPublications  int
	receivedEvents        []domain.SubmissionReceivedEvent
	transactionSeen       bool
	transactionFailure    string
	transactionBarrier    bool
	transactionArrivals   int
	transactionsReleased  chan struct{}
	pendingValues         map[string]json.RawMessage
	duplicateReadCount    int
	lateReadReady         chan struct{}
	lateVisibilityEnabled chan struct{}
}

func newFakeSubmissionState() *fakeSubmissionState {
	return &fakeSubmissionState{
		values:               make(map[string]json.RawMessage),
		legacyReadsReleased:  make(chan struct{}),
		transactionsReleased: make(chan struct{}),
		pendingValues:        make(map[string]json.RawMessage),
	}
}

func (f *fakeSubmissionState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	const statePrefix = "/v1.0/state/statestore/"

	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, statePrefix) {
		key, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, statePrefix))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if strings.HasPrefix(key, "duplicate:") {
			f.mu.Lock()
			if !f.transactionSeen {
				f.legacyDuplicateReads++
				if f.legacyDuplicateReads == 2 {
					close(f.legacyReadsReleased)
				}
				release := f.legacyReadsReleased
				f.mu.Unlock()
				<-release
			} else {
				f.mu.Unlock()
			}
		}

		f.mu.Lock()
		value, found := f.values[key]
		if strings.HasPrefix(key, "duplicate:") && f.transactionFailure == "late_visibility" {
			f.duplicateReadCount++
			if f.duplicateReadCount == 1 && !found {
				if f.lateReadReady != nil {
					close(f.lateReadReady)
					<-f.lateVisibilityEnabled
				}
				for pendingKey, pendingValue := range f.pendingValues {
					f.values[pendingKey] = append(json.RawMessage(nil), pendingValue...)
				}
			}
		}
		f.mu.Unlock()
		if !found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(value)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/v1.0/state/statestore/transaction" {
		var request struct {
			Operations []struct {
				Operation string `json:"operation"`
				Request   struct {
					Key     string          `json:"key"`
					Value   json.RawMessage `json:"value"`
					Options struct {
						Concurrency string `json:"concurrency"`
					} `json:"options"`
				} `json:"request"`
			} `json:"operations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		f.mu.Lock()
		f.transactionSeen = true
		if f.transactionBarrier {
			f.transactionArrivals++
			if f.transactionArrivals == 2 {
				close(f.transactionsReleased)
			}
			release := f.transactionsReleased
			f.mu.Unlock()
			select {
			case <-release:
			case <-r.Context().Done():
				http.Error(w, "transaction barrier canceled", http.StatusGatewayTimeout)
				return
			}
			f.mu.Lock()
		}
		for _, operation := range request.Operations {
			if operation.Operation != "upsert" {
				f.mu.Unlock()
				http.Error(w, "unsupported transaction operation", http.StatusBadRequest)
				return
			}
			if operation.Request.Options.Concurrency == "first-write" {
				if _, exists := f.values[operation.Request.Key]; exists {
					f.mu.Unlock()
					http.Error(w, "transaction conflict", http.StatusInternalServerError)
					return
				}
			}
		}
		if f.transactionFailure == "before_commit" {
			f.mu.Unlock()
			http.Error(w, "transaction outcome unknown", http.StatusInternalServerError)
			return
		}
		if f.transactionFailure == "late_visibility" {
			for _, operation := range request.Operations {
				f.pendingValues[operation.Request.Key] = append(json.RawMessage(nil), operation.Request.Value...)
			}
			f.mu.Unlock()
			http.Error(w, "transaction outcome unknown", http.StatusInternalServerError)
			return
		}
		for _, operation := range request.Operations {
			f.values[operation.Request.Key] = append(json.RawMessage(nil), operation.Request.Value...)
		}
		failure := f.transactionFailure
		f.mu.Unlock()
		if failure == "after_commit" {
			http.Error(w, "transaction outcome unknown", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/v1.0/state/statestore" {
		var items []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		for _, item := range items {
			f.values[item.Key] = append(json.RawMessage(nil), item.Value...)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/v1.0/publish/pubsub/submission.received" {
		var event domain.SubmissionReceivedEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.receivedPublications++
		f.receivedEvents = append(f.receivedEvents, event)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/v1.0/publish/pubsub/audit.event" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Error(w, fmt.Sprintf("unexpected request: %s %s", r.Method, r.URL.String()), http.StatusNotFound)
}

func submissionServerForTest(t *testing.T, fake *fakeSubmissionState) (*server, func()) {
	t.Helper()
	sidecar := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	sidecarURL, err := url.Parse(sidecar.URL)
	if err != nil {
		sidecar.Close()
		t.Fatalf("parse fake sidecar URL: %v", err)
	}
	_, port, found := strings.Cut(sidecarURL.Host, ":")
	if !found {
		sidecar.Close()
		t.Fatalf("fake sidecar URL has no port: %s", sidecar.URL)
	}
	t.Setenv("DAPR_HTTP_PORT", port)
	return &server{
		log:  logger.New(serviceName),
		dapr: daprclient.NewFromEnv(),
	}, sidecar.Close
}

func performSubmissionRequest(t *testing.T, srv *server) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(validSubmissionRequestForTest())
	if err != nil {
		t.Fatalf("marshal submission request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/submissions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	srv.handleSubmissions(recorder, req)
	return recorder
}

func TestConcurrentDuplicateSubmissionsHaveOneAuthoritativeTrackingID(t *testing.T) {
	fake := newFakeSubmissionState()
	fake.transactionBarrier = true
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()
	payload, err := json.Marshal(validSubmissionRequestForTest())
	if err != nil {
		t.Fatalf("marshal submission request: %v", err)
	}

	type result struct {
		status   int
		response domain.SubmissionResponse
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			requestContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/submissions", bytes.NewReader(payload)).WithContext(requestContext)
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			srv.handleSubmissions(recorder, req)

			var response domain.SubmissionResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Errorf("decode submission response (%d %s): %v", recorder.Code, recorder.Body.String(), err)
			}
			results <- result{status: recorder.Code, response: response}
		}()
	}
	close(start)
	var first, second result
	select {
	case first = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first concurrent submission")
	}
	select {
	case second = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second concurrent submission")
	}

	var winner, loser domain.SubmissionResponse
	switch {
	case first.status == http.StatusAccepted && second.status == http.StatusOK:
		winner, loser = first.response, second.response
	case second.status == http.StatusAccepted && first.status == http.StatusOK:
		winner, loser = second.response, first.response
	default:
		t.Fatalf("expected one 202 winner and one 200 duplicate, got statuses %d and %d", first.status, second.status)
	}
	if winner.TrackingID == "" || winner.Duplicate {
		t.Fatalf("expected 202 winner with duplicate=false, got %+v", winner)
	}
	if !loser.Duplicate || loser.TrackingID != winner.TrackingID || loser.DuplicateOf != winner.TrackingID {
		t.Fatalf("expected 200 loser referencing winner %q, got %+v", winner.TrackingID, loser)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.receivedPublications != 1 {
		t.Fatalf("expected one submission.received publication, got %d", fake.receivedPublications)
	}
	if len(fake.receivedEvents) != 1 || fake.receivedEvents[0].TrackingID != winner.TrackingID {
		t.Fatalf("expected one publication for winner %q, got %+v", winner.TrackingID, fake.receivedEvents)
	}
	fingerprintKey := "duplicate:" + duplicateFingerprint(validSubmissionRequestForTest())
	var claimedTrackingID string
	if err := json.Unmarshal(fake.values[fingerprintKey], &claimedTrackingID); err != nil {
		t.Fatalf("decode duplicate claim: %v", err)
	}
	if claimedTrackingID != winner.TrackingID {
		t.Fatalf("expected duplicate claim %q, got %q", winner.TrackingID, claimedTrackingID)
	}
	submissionRecords := 0
	var storedRecord domain.SubmissionRecord
	for key := range fake.values {
		if strings.HasPrefix(key, "submission:") {
			submissionRecords++
			if err := json.Unmarshal(fake.values[key], &storedRecord); err != nil {
				t.Fatalf("decode stored submission %s: %v", key, err)
			}
		}
	}
	if submissionRecords != 1 {
		t.Fatalf("expected one submission record, got %d", submissionRecords)
	}
	if storedRecord.TrackingID != winner.TrackingID {
		t.Fatalf("expected stored record tracking ID %q, got %q", winner.TrackingID, storedRecord.TrackingID)
	}
	if fake.transactionArrivals != 2 {
		t.Fatalf("expected two transaction arrivals before application, got %d", fake.transactionArrivals)
	}
}

func TestSequentialDuplicateSubmissionReturnsExistingTrackingID(t *testing.T) {
	fake := newFakeSubmissionState()
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()

	first := performSubmissionRequest(t, srv)
	second := performSubmissionRequest(t, srv)
	if first.Code != http.StatusAccepted || second.Code != http.StatusOK {
		t.Fatalf("expected statuses 202 then 200, got %d then %d", first.Code, second.Code)
	}

	var accepted, duplicate domain.SubmissionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if accepted.TrackingID == "" || duplicate.TrackingID != accepted.TrackingID || !duplicate.Duplicate {
		t.Fatalf("expected duplicate to return %q, got %+v", accepted.TrackingID, duplicate)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.receivedPublications != 1 {
		t.Fatalf("expected one submission.received publication, got %d", fake.receivedPublications)
	}
}

func TestSubmissionReconcilesCommittedTransactionErrorAsWinner(t *testing.T) {
	fake := newFakeSubmissionState()
	fake.transactionFailure = "after_commit"
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()

	recorder := performSubmissionRequest(t, srv)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected reconciled winner status 202, got %d: %s", recorder.Code, recorder.Body.String())
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.receivedPublications != 1 {
		t.Fatalf("expected reconciled winner to publish once, got %d", fake.receivedPublications)
	}
}

func TestSubmissionPropagatesTransactionErrorWithoutAuthoritativeState(t *testing.T) {
	fake := newFakeSubmissionState()
	fake.transactionFailure = "before_commit"
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()

	recorder := performSubmissionRequest(t, srv)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected infrastructure error status 500, got %d: %s", recorder.Code, recorder.Body.String())
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.receivedPublications != 0 {
		t.Fatalf("expected no publication without authoritative state, got %d", fake.receivedPublications)
	}
	if len(fake.values) != 0 {
		t.Fatalf("expected no state after pre-commit failure, got %v", fake.values)
	}
}

func TestSubmissionReconcilesLateVisibleTransactionState(t *testing.T) {
	fake := newFakeSubmissionState()
	fake.transactionFailure = "late_visibility"
	fake.lateReadReady = make(chan struct{})
	fake.lateVisibilityEnabled = make(chan struct{})
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()

	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- performSubmissionRequest(t, srv) }()
	select {
	case <-fake.lateReadReady:
		close(fake.lateVisibilityEnabled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first reconciliation read")
	}

	var recorder *httptest.ResponseRecorder
	select {
	case received := <-result:
		recorder = received
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconciled submission")
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected late-visible winner status 202, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response domain.SubmissionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode late-visible response: %v", err)
	}
	if response.TrackingID == "" || response.Duplicate {
		t.Fatalf("expected late-visible winner response, got %+v", response)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.receivedPublications != 1 || len(fake.receivedEvents) != 1 {
		t.Fatalf("expected exactly one reconciled publication, got count=%d events=%+v", fake.receivedPublications, fake.receivedEvents)
	}
	if fake.receivedEvents[0].TrackingID != response.TrackingID {
		t.Fatalf("expected publication tracking ID %q, got %q", response.TrackingID, fake.receivedEvents[0].TrackingID)
	}
	fingerprintKey := "duplicate:" + duplicateFingerprint(validSubmissionRequestForTest())
	var claimedTrackingID string
	if err := json.Unmarshal(fake.values[fingerprintKey], &claimedTrackingID); err != nil || claimedTrackingID != response.TrackingID {
		t.Fatalf("expected authoritative claim %q, got %q (err=%v)", response.TrackingID, claimedTrackingID, err)
	}
}

func validSubmissionRequestForTest() domain.SubmissionRequest {
	return domain.SubmissionRequest{
		ID:             "INV-TEST-001",
		Submitter:      "alice@example.com",
		Department:     "engineering",
		Vendor:         "Known Vendor",
		VendorKnown:    true,
		InvoiceNumber:  "INV-001",
		Currency:       "USD",
		Category:       "saas",
		Total:          99,
		ReceiptPresent: true,
		Date:           "2026-05-30",
	}
}

func TestValidateSubmissionAcceptsValidRequest(t *testing.T) {
	req := validSubmissionRequestForTest()

	if err := validateSubmission(req); err != nil {
		t.Fatalf("expected valid submission, got error: %v", err)
	}
}

func TestValidateSubmissionRejectsInvalidRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SubmissionRequest)
	}{
		{
			name: "missing vendor",
			mutate: func(req *domain.SubmissionRequest) {
				req.Vendor = ""
			},
		},
		{
			name: "missing invoice number",
			mutate: func(req *domain.SubmissionRequest) {
				req.InvoiceNumber = ""
			},
		},
		{
			name: "zero total",
			mutate: func(req *domain.SubmissionRequest) {
				req.Total = 0
			},
		},
		{
			name: "negative total",
			mutate: func(req *domain.SubmissionRequest) {
				req.Total = -10
			},
		},
		{
			name: "missing currency",
			mutate: func(req *domain.SubmissionRequest) {
				req.Currency = ""
			},
		},
		{
			name: "missing category",
			mutate: func(req *domain.SubmissionRequest) {
				req.Category = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validSubmissionRequestForTest()
			tt.mutate(&req)

			if err := validateSubmission(req); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestDuplicateFingerprintIsStableAndNormalized(t *testing.T) {
	left := validSubmissionRequestForTest()
	left.Vendor = "  Known Vendor  "
	left.InvoiceNumber = " Inv-001 "
	left.Total = 99

	right := validSubmissionRequestForTest()
	right.Vendor = "known vendor"
	right.InvoiceNumber = "inv-001"
	right.Total = 99

	if duplicateFingerprint(left) != duplicateFingerprint(right) {
		t.Fatalf("expected duplicate fingerprint to normalize vendor and invoice number")
	}
}

func TestDuplicateFingerprintChangesWhenBusinessIdentityChanges(t *testing.T) {
	base := validSubmissionRequestForTest()

	changedVendor := base
	changedVendor.Vendor = "Other Vendor"

	changedInvoice := base
	changedInvoice.InvoiceNumber = "INV-002"

	changedTotal := base
	changedTotal.Total = 100

	baseFingerprint := duplicateFingerprint(base)

	if duplicateFingerprint(changedVendor) == baseFingerprint {
		t.Fatalf("expected vendor change to change duplicate fingerprint")
	}

	if duplicateFingerprint(changedInvoice) == baseFingerprint {
		t.Fatalf("expected invoice number change to change duplicate fingerprint")
	}

	if duplicateFingerprint(changedTotal) == baseFingerprint {
		t.Fatalf("expected total change to change duplicate fingerprint")
	}
}

func TestApplyAdditionalInfoToRecord(t *testing.T) {
	notes := "Client: Contoso. Business justification added."
	receiptPresent := true
	attendees := 11

	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(&record, domain.AdditionalInfoRequest{
		Notes:          &notes,
		ReceiptPresent: &receiptPresent,
		Attendees:      &attendees,
	})
	if err != nil {
		t.Fatalf("expected additional info to be accepted: %v", err)
	}

	if record.Request.Notes != notes {
		t.Fatalf("expected notes to be updated")
	}

	if !record.Request.ReceiptPresent {
		t.Fatalf("expected receiptPresent to be updated")
	}

	if record.Request.Attendees == nil || *record.Request.Attendees != attendees {
		t.Fatalf("expected attendees to be updated")
	}
}

func TestApplyAdditionalInfoToRecordRequiresAtLeastOneField(t *testing.T) {
	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(
		&record,
		domain.AdditionalInfoRequest{},
	)
	if err == nil {
		t.Fatalf("expected empty additional info payload to be rejected")
	}
}

func TestApplyAdditionalInfoToRecordRejectsInvalidAttendees(t *testing.T) {
	attendees := 0
	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(
		&record,
		domain.AdditionalInfoRequest{
			Attendees: &attendees,
		},
	)
	if err == nil {
		t.Fatalf("expected non-positive attendees to be rejected")
	}
}

func TestSubmissionEventsCarryPublishedRevision(t *testing.T) {
	fake := newFakeSubmissionState()
	srv, closeSidecar := submissionServerForTest(t, fake)
	defer closeSidecar()
	w := performSubmissionRequest(t, srv)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	fake.mu.Lock()
	if len(fake.receivedEvents) != 1 {
		fake.mu.Unlock()
		t.Fatal("expected initial event")
	}
	initial := fake.receivedEvents[0]
	var record domain.SubmissionRecord
	err := json.Unmarshal(fake.values["submission:"+initial.TrackingID], &record)
	if err != nil {
		fake.mu.Unlock()
		t.Fatal(err)
	}
	if initial.RevisionNumber != 1 || initial.RevisionNumber != record.RevisionNumber {
		fake.mu.Unlock()
		t.Fatalf("initial event revision: %+v", initial)
	}
	record.Status = domain.SubmissionInfoRequested
	fake.values["submission:"+initial.TrackingID], err = json.Marshal(record)
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/submissions/"+initial.TrackingID+"/additional-info", strings.NewReader(`{"notes":"Additional business justification"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.applyAdditionalInfo(w, req, initial.TrackingID)
	if w.Code < 200 || w.Code >= 300 {
		t.Fatal(w.Code, w.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.receivedEvents) != 2 {
		t.Fatal("expected resubmission event")
	}
	updated := fake.receivedEvents[1]
	if err := json.Unmarshal(fake.values["submission:"+initial.TrackingID], &record); err != nil {
		t.Fatal(err)
	}
	if updated.RevisionNumber != 2 || updated.RevisionNumber != record.RevisionNumber || updated.TrackingID != initial.TrackingID || fake.receivedEvents[0].RevisionNumber != 1 {
		t.Fatalf("wrong published revisions: %+v", fake.receivedEvents)
	}
}
