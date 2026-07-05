package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/auditlog"
	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
)

const serviceName = "submission-service"
const stateStore = "statestore"

type server struct {
	log  *logger.Logger
	dapr *daprclient.Client
}

func main() {
	log := logger.New(serviceName)
	port := config.GetEnv("PORT", "8080")

	srv := &server{
		log:  log,
		dapr: daprclient.NewFromEnv(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))
	mux.HandleFunc("/submissions", srv.handleSubmissions)
	mux.HandleFunc("/submissions/", srv.handleSubmissionByID)
	mux.HandleFunc("/internal/submissions/", srv.handleInternalSubmission)

	handler := httpx.CorrelationMiddleware(mux)

	addr := ":" + port
	log.Info("service starting", logger.Fields{"addr": addr})

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Error("service failed", logger.Fields{"error": err.Error()})
	}
}

func (s *server) handleSubmissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req domain.SubmissionRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid submission payload")
		return
	}

	if err := validateSubmission(req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	duplicateKey := "duplicate:" + duplicateFingerprint(req)

	var existingTrackingID string
	found, err := s.dapr.GetState(r.Context(), stateStore, duplicateKey, &existingTrackingID)
	if err != nil {
		s.log.Error("failed to check duplicate state", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to check duplicate state")
		return
	}

	if found && existingTrackingID != "" {
		recordKey := "submission:" + existingTrackingID

		var existing domain.SubmissionRecord
		foundRecord, err := s.dapr.GetState(r.Context(), stateStore, recordKey, &existing)
		if err != nil || !foundRecord {
			httpx.WriteError(w, r, http.StatusInternalServerError, "duplicate record is inconsistent")
			return
		}

		response := domain.SubmissionResponse{
			TrackingID:  existing.TrackingID,
			Status:      domain.SubmissionDuplicate,
			Reason:      "Duplicate submission detected by vendor + invoiceNumber + total. Existing tracking id returned; no second processing will be started.",
			Duplicate:   true,
			DuplicateOf: existing.TrackingID,
		}

		if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
			EventID:       "audit_" + existing.TrackingID + "_duplicate_submission_detected",
			CorrelationID: httpx.CorrelationIDFromContext(r.Context()),
			TrackingID:    existing.TrackingID,
			InvoiceID:     existing.OriginalInvoiceID,
			Service:       serviceName,
			Action:        "duplicate_submission_detected",
			Outcome:       "duplicate",
			Reason:        response.Reason,
			Fields: map[string]any{
				"duplicate_of":  existing.TrackingID,
				"vendor":        req.Vendor,
				"invoiceNumber": req.InvoiceNumber,
				"total":         req.Total,
			},
		}); err != nil {
			s.log.Error("failed to publish audit event", logger.Fields{
				"error":          err.Error(),
				"tracking_id":    existing.TrackingID,
				"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
			})
		}

		httpx.WriteJSON(w, http.StatusOK, response)
		return
	}

	now := time.Now().UTC()
	trackingID := "sub_" + randomHex(12)

	record := domain.SubmissionRecord{
		TrackingID:        trackingID,
		OriginalInvoiceID: req.ID,
		Status:            domain.SubmissionAccepted,
		Reason:            "Submission accepted for asynchronous processing.",
		Duplicate:         false,
		Request:           req,
		CreatedAtUTC:      now,
		UpdatedAtUTC:      now,
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, "submission:"+trackingID, record); err != nil {
		s.log.Error("failed to save submission", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save submission")
		return
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, duplicateKey, trackingID); err != nil {
		s.log.Error("failed to save duplicate key", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save duplicate key")
		return
	}

	event := domain.SubmissionReceivedEvent{
		EventID:       "evt_" + randomHex(12),
		EventType:     domain.TopicSubmissionReceived,
		CorrelationID: httpx.CorrelationIDFromContext(r.Context()),
		TrackingID:    trackingID,
		InvoiceID:     req.ID,
		OccurredAtUTC: now,
	}

	if err := s.dapr.PublishEvent(r.Context(), domain.PubSubName, domain.TopicSubmissionReceived, event); err != nil {
		s.log.Error("failed to publish submission received event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    trackingID,
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to publish submission event")
		return
	}

	s.log.Info("submission accepted and event published", logger.Fields{
		"tracking_id":    trackingID,
		"event_id":       event.EventID,
		"correlation_id": event.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID:       "audit_" + trackingID + "_submission_accepted",
		CorrelationID: event.CorrelationID,
		TrackingID:    trackingID,
		InvoiceID:     req.ID,
		Service:       serviceName,
		Action:        "submission_accepted",
		Outcome:       string(record.Status),
		Reason:        record.Reason,
		Fields: map[string]any{
			"vendor":        req.Vendor,
			"invoiceNumber": req.InvoiceNumber,
			"category":      req.Category,
			"currency":      req.Currency,
			"total":         req.Total,
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    trackingID,
			"correlation_id": event.CorrelationID,
		})
	}

	response := domain.SubmissionResponse{
		TrackingID: trackingID,
		Status:     record.Status,
		Reason:     record.Reason,
		Duplicate:  false,
	}

	httpx.WriteJSON(w, http.StatusAccepted, response)
}

func (s *server) handleSubmissionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	trackingID := strings.TrimPrefix(r.URL.Path, "/submissions/")
	if trackingID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing tracking id")
		return
	}

	s.getSubmissionRecord(w, r, trackingID)
}

func (s *server) handleInternalSubmission(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/internal/submissions/")
	if path == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing tracking id")
		return
	}

	if strings.HasSuffix(path, "/decision") {
		trackingID := strings.TrimSuffix(path, "/decision")
		trackingID = strings.TrimSuffix(trackingID, "/")

		if r.Method != http.MethodPost {
			httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		s.applyDecision(w, r, trackingID)
		return
	}

	if strings.HasSuffix(path, "/payment") {
		trackingID := strings.TrimSuffix(path, "/payment")
		trackingID = strings.TrimSuffix(trackingID, "/")

		if r.Method != http.MethodPost {
			httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		s.applyPayment(w, r, trackingID)
		return
	}

	if strings.HasSuffix(path, "/approval") {
		trackingID := strings.TrimSuffix(path, "/approval")
		trackingID = strings.TrimSuffix(trackingID, "/")

		if r.Method != http.MethodPost {
			httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		s.applyApproval(w, r, trackingID)
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	trackingID := strings.Trim(path, "/")
	s.getSubmissionRecord(w, r, trackingID)
}

func (s *server) getSubmissionRecord(w http.ResponseWriter, r *http.Request, trackingID string) {
	var record domain.SubmissionRecord
	found, err := s.dapr.GetState(r.Context(), stateStore, "submission:"+trackingID, &record)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to read submission")
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "submission not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, record)
}

func (s *server) applyDecision(w http.ResponseWriter, r *http.Request, trackingID string) {
	var req domain.ApplyDecisionRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid decision payload")
		return
	}

	var record domain.SubmissionRecord
	found, err := s.dapr.GetState(r.Context(), stateStore, "submission:"+trackingID, &record)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to read submission")
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "submission not found")
		return
	}

	record.Status = req.Status
	record.Reason = req.Reason
	record.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, "submission:"+trackingID, record); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, record)
}

func (s *server) applyPayment(w http.ResponseWriter, r *http.Request, trackingID string) {
	var req domain.ApplyPaymentRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid payment payload")
		return
	}

	var record domain.SubmissionRecord
	found, err := s.dapr.GetState(r.Context(), stateStore, "submission:"+trackingID, &record)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to read submission")
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "submission not found")
		return
	}

	record.Status = req.Status
	record.Reason = req.Reason
	record.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, "submission:"+trackingID, record); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission payment status")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, record)
}

func (s *server) applyApproval(w http.ResponseWriter, r *http.Request, trackingID string) {
	var req domain.ApplyApprovalRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid approval payload")
		return
	}

	var record domain.SubmissionRecord
	found, err := s.dapr.GetState(r.Context(), stateStore, "submission:"+trackingID, &record)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to read submission")
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "submission not found")
		return
	}

	record.Status = req.Status
	record.Reason = req.Reason
	record.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, "submission:"+trackingID, record); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission approval status")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, record)
}

func validateSubmission(req domain.SubmissionRequest) error {
	if strings.TrimSpace(req.Vendor) == "" {
		return fmt.Errorf("vendor is required")
	}
	if strings.TrimSpace(req.InvoiceNumber) == "" {
		return fmt.Errorf("invoiceNumber is required")
	}
	if strings.TrimSpace(req.Currency) == "" {
		return fmt.Errorf("currency is required")
	}
	if strings.TrimSpace(req.Category) == "" {
		return fmt.Errorf("category is required")
	}
	if req.Total <= 0 {
		return fmt.Errorf("total must be positive")
	}
	return nil
}

func duplicateFingerprint(req domain.SubmissionRequest) string {
	normalized := strings.ToLower(strings.TrimSpace(req.Vendor)) +
		"|" + strings.ToLower(strings.TrimSpace(req.InvoiceNumber)) +
		"|" + strconv.FormatFloat(req.Total, 'f', 2, 64)

	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(out)
}
