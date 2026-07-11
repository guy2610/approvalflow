package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
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

const serviceName = "approval-service"
const stateStore = "statestore"

type server struct {
	log  *logger.Logger
	dapr *daprclient.Client
}

type daprSubscription struct {
	PubSubName string `json:"pubsubname"`
	Topic      string `json:"topic"`
	Route      string `json:"route"`
}

type daprCloudEvent struct {
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	SpecVersion     string          `json:"specversion"`
	DataContentType string          `json:"datacontenttype"`
	TraceID         string          `json:"traceid"`
	Data            json.RawMessage `json:"data"`
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
	mux.HandleFunc("/dapr/subscribe", srv.handleDaprSubscribe)
	mux.HandleFunc("/events/approval-required", srv.handleApprovalRequired)
	mux.HandleFunc("/approvals", srv.handleApprovals)
	mux.HandleFunc("/approvals/", srv.handleApprovalAction)

	handler := httpx.CorrelationMiddleware(mux)

	addr := ":" + port
	log.Info("service starting", logger.Fields{"addr": addr})

	if err := httpx.NewServer(addr, handler).ListenAndServe(); err != nil {
		log.Error("service failed", logger.Fields{"error": err.Error()})
	}
}

func (s *server) handleDaprSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	subscriptions := []daprSubscription{
		{
			PubSubName: domain.PubSubName,
			Topic:      domain.TopicApprovalRequired,
			Route:      "/events/approval-required",
		},
	}

	httpx.WriteJSON(w, http.StatusOK, subscriptions)
}

func (s *server) handleApprovalRequired(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()

	var envelope daprCloudEvent
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid cloud event")
		return
	}

	var event domain.ApprovalRequiredEvent
	if err := json.Unmarshal(envelope.Data, &event); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid approval event")
		return
	}

	key := approvalKey(event.TrackingID)

	var existing domain.ApprovalItem
	found, err := s.dapr.GetState(r.Context(), stateStore, key, &existing)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to check approval item")
		return
	}

	if found {
		disposition := classifyApprovalRequiredEvent(existing, event)

		switch disposition {
		case approvalEventReopen:
			reopened := reopenApprovalItem(existing, event)

			if err := s.dapr.SaveState(
				r.Context(),
				stateStore,
				key,
				reopened,
			); err != nil {
				httpx.WriteError(
					w,
					r,
					http.StatusInternalServerError,
					"failed to reopen approval item",
				)
				return
			}

			s.log.Info(
				"approval item reopened after additional information",
				logger.Fields{
					"tracking_id":     reopened.TrackingID,
					"invoice_id":      reopened.InvoiceID,
					"amount_usd":      reopened.AmountUSD,
					"revision_number": reopened.RevisionNumber,
					"source_event_id": reopened.SourceEventID,
					"violations":      reopened.Violations,
					"correlation_id":  reopened.CorrelationID,
				},
			)

			if err := auditlog.Publish(
				r.Context(),
				s.dapr,
				auditlog.Event{
					EventID: "audit_" +
						reopened.TrackingID +
						"_approval_item_reopened_" +
						event.EventID,
					CorrelationID: reopened.CorrelationID,
					TrackingID:    reopened.TrackingID,
					InvoiceID:     reopened.InvoiceID,
					Service:       serviceName,
					Action:        "approval_item_reopened",
					Outcome:       string(reopened.Status),
					Reason:        reopened.Reason,
					Fields: map[string]any{
						"amount_usd":        reopened.AmountUSD,
						"revision_number":   reopened.RevisionNumber,
						"source_event_id":   reopened.SourceEventID,
						"violations":        reopened.Violations,
						"agent_recommended": reopened.AgentRecommended,
						"agent_confidence":  reopened.AgentConfidence,
						"agent_cited_rules": reopened.AgentCitedRules,
					},
				},
			); err != nil {
				s.log.Error(
					"failed to publish approval reopen audit event",
					logger.Fields{
						"error":          err.Error(),
						"tracking_id":    reopened.TrackingID,
						"correlation_id": reopened.CorrelationID,
					},
				)
			}

		case approvalEventStale:
			s.log.Info(
				"stale approval required event ignored",
				logger.Fields{
					"tracking_id":              event.TrackingID,
					"incoming_event_id":        event.EventID,
					"incoming_revision_number": event.RevisionNumber,
					"stored_event_id":          existing.SourceEventID,
					"stored_revision_number":   existing.RevisionNumber,
					"approval_status":          existing.Status,
					"correlation_id":           event.CorrelationID,
				},
			)

		case approvalEventIgnore:
			s.log.Info(
				"newer approval event ignored for incompatible approval state",
				logger.Fields{
					"tracking_id":              event.TrackingID,
					"incoming_event_id":        event.EventID,
					"incoming_revision_number": event.RevisionNumber,
					"stored_event_id":          existing.SourceEventID,
					"stored_revision_number":   existing.RevisionNumber,
					"approval_status":          existing.Status,
					"correlation_id":           event.CorrelationID,
				},
			)

		default:
			s.log.Info(
				"duplicate approval required event ignored",
				logger.Fields{
					"tracking_id":              event.TrackingID,
					"incoming_event_id":        event.EventID,
					"incoming_revision_number": event.RevisionNumber,
					"stored_event_id":          existing.SourceEventID,
					"stored_revision_number":   existing.RevisionNumber,
					"approval_status":          existing.Status,
					"correlation_id":           event.CorrelationID,
				},
			)
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	now := time.Now().UTC()
	item := domain.ApprovalItem{
		TrackingID:       event.TrackingID,
		InvoiceID:        event.InvoiceID,
		SourceEventID:    event.EventID,
		RevisionNumber:   event.RevisionNumber,
		AmountUSD:        event.AmountUSD,
		Status:           domain.ApprovalPending,
		Reason:           event.Reason,
		Violations:       event.Violations,
		AgentRecommended: event.AgentRecommended,
		AgentConfidence:  event.AgentConfidence,
		AgentCitedRules:  event.AgentCitedRules,
		CorrelationID:    event.CorrelationID,
		CreatedAtUTC:     now,
		UpdatedAtUTC:     now,
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, key, item); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save approval item")
		return
	}

	if err := s.addToApprovalIndex(r, event.TrackingID); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update approval index")
		return
	}

	s.log.Info("approval item queued", logger.Fields{
		"tracking_id":    item.TrackingID,
		"invoice_id":     item.InvoiceID,
		"amount_usd":     item.AmountUSD,
		"violations":     item.Violations,
		"correlation_id": item.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID:       "audit_" + item.TrackingID + "_approval_item_queued",
		CorrelationID: item.CorrelationID,
		TrackingID:    item.TrackingID,
		InvoiceID:     item.InvoiceID,
		Service:       serviceName,
		Action:        "approval_item_queued",
		Outcome:       string(item.Status),
		Reason:        item.Reason,
		Fields: map[string]any{
			"amount_usd":        item.AmountUSD,
			"violations":        item.Violations,
			"agent_recommended": item.AgentRecommended,
			"agent_confidence":  item.AgentConfidence,
			"agent_cited_rules": item.AgentCitedRules,
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    item.TrackingID,
			"correlation_id": item.CorrelationID,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ids, err := s.loadApprovalIndex(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load approvals")
		return
	}

	items := make([]domain.ApprovalItem, 0, len(ids))
	for _, id := range ids {
		var item domain.ApprovalItem
		found, err := s.dapr.GetState(r.Context(), stateStore, approvalKey(id), &item)
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load approval item")
			return
		}
		if found {
			items = append(items, item)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAtUTC.Before(items[j].CreatedAtUTC)
	})

	httpx.WriteJSON(w, http.StatusOK, domain.ApprovalListResponse{Items: items})
}

func (s *server) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/approvals/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		httpx.WriteError(w, r, http.StatusBadRequest, "expected /approvals/{tracking_id}/{action}")
		return
	}

	trackingID := parts[0]
	action := parts[1]

	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req domain.ApprovalActionRequest
	if err := httpx.DecodeJSON(
		w,
		r,
		&req,
		httpx.DefaultMaxJSONBodyBytes,
	); err != nil {
		httpx.WriteJSONDecodeError(w, r, err)
		return
	}
	defer r.Body.Close()

	if req.Actor == "" {
		req.Actor = "demo.approver@northwind.example"
	}

	switch action {
	case "approve":
		s.approve(w, r, trackingID, req)
	case "reject":
		s.reject(w, r, trackingID, req)
	case "request-info":
		s.requestInfo(w, r, trackingID, req)
	default:
		httpx.WriteError(w, r, http.StatusNotFound, "unknown approval action")
	}
}

func (s *server) approve(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
	req domain.ApprovalActionRequest,
) {
	item, record, shouldSaveDecision, etag, ok :=
		s.loadApprovalForActionOrWriteError(
			w,
			r,
			trackingID,
			"approve",
		)
	if !ok {
		return
	}

	if shouldSaveDecision {
		item.Status = domain.ApprovalApproved
		item.ActionBy = req.Actor
		item.ActionReason = req.Reason
		item.UpdatedAtUTC = time.Now().UTC()

		item, record, ok =
			s.saveApprovalDecisionOrReconcileConflict(
				w,
				r,
				trackingID,
				"approve",
				item,
				etag,
			)
		if !ok {
			return
		}
	}

	if shouldApplySubmissionApproval("approve", record.Status) {
		if err := s.applySubmissionApproval(
			r,
			trackingID,
			domain.SubmissionApprovedByHuman,
			"Human approver approved the submission.",
			item.ActionBy,
		); err != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to update submission approval",
			)
			return
		}

		record.Status = domain.SubmissionApprovedByHuman
	}

	if shouldPublishApprovalPayment(record.Status) {
		paymentEvent := domain.PaymentRequestedEvent{
			EventID: approvalEffectEventID(
				item,
				"evt_approval_payment",
			),
			EventType:     domain.TopicPaymentRequested,
			CorrelationID: item.CorrelationID,
			TrackingID:    trackingID,
			InvoiceID:     item.InvoiceID,
			AmountUSD:     item.AmountUSD,
			OccurredAtUTC: item.UpdatedAtUTC,
		}

		if err := s.dapr.PublishEvent(
			r.Context(),
			domain.PubSubName,
			domain.TopicPaymentRequested,
			paymentEvent,
		); err != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to publish payment request",
			)
			return
		}
	}

	s.log.Info("approval accepted and downstream effects reconciled", logger.Fields{
		"tracking_id":       trackingID,
		"invoice_id":        item.InvoiceID,
		"amount_usd":        item.AmountUSD,
		"actor":             item.ActionBy,
		"submission_status": record.Status,
		"correlation_id":    item.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID: approvalEffectEventID(
			item,
			"audit_human_approved",
		),
		CorrelationID: item.CorrelationID,
		TrackingID:    trackingID,
		InvoiceID:     item.InvoiceID,
		Service:       serviceName,
		Action:        "human_approved",
		Outcome:       string(item.Status),
		Reason:        item.ActionReason,
		Fields: map[string]any{
			"actor":      item.ActionBy,
			"amount_usd": item.AmountUSD,
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    trackingID,
			"correlation_id": item.CorrelationID,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) reject(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
	req domain.ApprovalActionRequest,
) {
	item, record, shouldSaveDecision, etag, ok :=
		s.loadApprovalForActionOrWriteError(
			w,
			r,
			trackingID,
			"reject",
		)
	if !ok {
		return
	}

	if shouldSaveDecision {
		item.Status = domain.ApprovalRejected
		item.ActionBy = req.Actor
		item.ActionReason = req.Reason
		item.UpdatedAtUTC = time.Now().UTC()

		item, record, ok =
			s.saveApprovalDecisionOrReconcileConflict(
				w,
				r,
				trackingID,
				"reject",
				item,
				etag,
			)
		if !ok {
			return
		}
	}

	reason := "Human approver rejected the submission."
	if item.ActionReason != "" {
		reason += " Reason: " + item.ActionReason
	}

	if shouldApplySubmissionApproval("reject", record.Status) {
		if err := s.applySubmissionApproval(
			r,
			trackingID,
			domain.SubmissionRejectedByHuman,
			reason,
			item.ActionBy,
		); err != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to update submission rejection",
			)
			return
		}
	}

	s.log.Info("approval rejection downstream effects reconciled", logger.Fields{
		"tracking_id":    trackingID,
		"invoice_id":     item.InvoiceID,
		"actor":          item.ActionBy,
		"correlation_id": item.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID: approvalEffectEventID(
			item,
			"audit_human_rejected",
		),
		CorrelationID: item.CorrelationID,
		TrackingID:    trackingID,
		InvoiceID:     item.InvoiceID,
		Service:       serviceName,
		Action:        "human_rejected",
		Outcome:       string(item.Status),
		Reason:        item.ActionReason,
		Fields: map[string]any{
			"actor": item.ActionBy,
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    trackingID,
			"correlation_id": item.CorrelationID,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) requestInfo(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
	req domain.ApprovalActionRequest,
) {
	item, record, shouldSaveDecision, etag, ok :=
		s.loadApprovalForActionOrWriteError(
			w,
			r,
			trackingID,
			"request-info",
		)
	if !ok {
		return
	}

	if shouldSaveDecision {
		item.Status = domain.ApprovalRequestInfo
		item.ActionBy = req.Actor
		item.ActionReason = req.Reason
		item.UpdatedAtUTC = time.Now().UTC()

		item, record, ok =
			s.saveApprovalDecisionOrReconcileConflict(
				w,
				r,
				trackingID,
				"request-info",
				item,
				etag,
			)
		if !ok {
			return
		}
	}

	reason := "Human approver requested more information."
	if item.ActionReason != "" {
		reason += " Reason: " + item.ActionReason
	}

	if shouldApplySubmissionApproval("request-info", record.Status) {
		if err := s.applySubmissionApproval(
			r,
			trackingID,
			domain.SubmissionInfoRequested,
			reason,
			item.ActionBy,
		); err != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to update submission request-info",
			)
			return
		}
	}

	s.log.Info("request-info downstream effects reconciled", logger.Fields{
		"tracking_id":    trackingID,
		"invoice_id":     item.InvoiceID,
		"actor":          item.ActionBy,
		"correlation_id": item.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID: approvalEffectEventID(
			item,
			"audit_info_requested",
		),
		CorrelationID: item.CorrelationID,
		TrackingID:    trackingID,
		InvoiceID:     item.InvoiceID,
		Service:       serviceName,
		Action:        "info_requested",
		Outcome:       string(item.Status),
		Reason:        item.ActionReason,
		Fields: map[string]any{
			"actor": item.ActionBy,
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    trackingID,
			"correlation_id": item.CorrelationID,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) loadSubmissionRecord(r *http.Request, trackingID string) (domain.SubmissionRecord, error) {
	var record domain.SubmissionRecord

	_, err := s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+trackingID,
		nil,
		&record,
	)

	return record, err
}

func (s *server) applySubmissionApproval(r *http.Request, trackingID string, status domain.SubmissionStatus, reason string, actor string) error {
	update := domain.ApplyApprovalRequest{
		Status: status,
		Reason: reason,
		Actor:  actor,
	}

	_, err := s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+trackingID+"/approval",
		update,
		nil,
	)
	return err
}

func (s *server) addToApprovalIndex(r *http.Request, trackingID string) error {
	ids, err := s.loadApprovalIndex(r)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if id == trackingID {
			return nil
		}
	}

	ids = append(ids, trackingID)
	return s.dapr.SaveState(r.Context(), stateStore, "approval:index", ids)
}

func (s *server) loadApprovalIndex(r *http.Request) ([]string, error) {
	var ids []string
	found, err := s.dapr.GetState(r.Context(), stateStore, "approval:index", &ids)
	if err != nil {
		return nil, err
	}
	if !found {
		return []string{}, nil
	}
	return ids, nil
}

func approvalKey(trackingID string) string {
	return "approval:" + trackingID
}

type approvalEventDisposition string

const (
	approvalEventDuplicate approvalEventDisposition = "duplicate"
	approvalEventStale     approvalEventDisposition = "stale"
	approvalEventReopen    approvalEventDisposition = "reopen"
	approvalEventIgnore    approvalEventDisposition = "ignore"
)

func classifyApprovalRequiredEvent(
	existing domain.ApprovalItem,
	event domain.ApprovalRequiredEvent,
) approvalEventDisposition {
	if existing.SourceEventID != "" &&
		event.EventID != "" &&
		existing.SourceEventID == event.EventID {
		return approvalEventDuplicate
	}

	storedRevision := existing.RevisionNumber
	if storedRevision <= 0 {
		storedRevision = 1
	}

	incomingRevision := event.RevisionNumber
	if incomingRevision <= 0 {
		incomingRevision = 1
	}

	if incomingRevision < storedRevision {
		return approvalEventStale
	}

	if incomingRevision == storedRevision {
		return approvalEventDuplicate
	}

	if existing.Status == domain.ApprovalRequestInfo {
		return approvalEventReopen
	}

	return approvalEventIgnore
}

func reopenApprovalItem(
	existing domain.ApprovalItem,
	event domain.ApprovalRequiredEvent,
) domain.ApprovalItem {
	existing.InvoiceID = event.InvoiceID
	existing.SourceEventID = event.EventID
	existing.RevisionNumber = event.RevisionNumber
	existing.AmountUSD = event.AmountUSD
	existing.Status = domain.ApprovalPending
	existing.Reason = event.Reason
	existing.Violations = event.Violations
	existing.AgentRecommended = event.AgentRecommended
	existing.AgentConfidence = event.AgentConfidence
	existing.AgentCitedRules = event.AgentCitedRules
	existing.CorrelationID = event.CorrelationID
	existing.ActionBy = ""
	existing.ActionReason = ""
	existing.UpdatedAtUTC = time.Now().UTC()

	return existing
}

func approvalStatusForAction(action string) (domain.ApprovalStatus, bool) {
	switch action {
	case "approve":
		return domain.ApprovalApproved, true
	case "reject":
		return domain.ApprovalRejected, true
	case "request-info":
		return domain.ApprovalRequestInfo, true
	default:
		return "", false
	}
}

func canReconcileApprovalAction(
	action string,
	submissionStatus domain.SubmissionStatus,
) bool {
	switch action {
	case "approve":
		switch submissionStatus {
		case domain.SubmissionHumanReviewRequired,
			domain.SubmissionApprovedByHuman,
			domain.SubmissionPaymentPending,
			domain.SubmissionPaid,
			domain.SubmissionPaymentFailed:
			return true
		}

	case "reject":
		switch submissionStatus {
		case domain.SubmissionHumanReviewRequired,
			domain.SubmissionRejectedByHuman:
			return true
		}

	case "request-info":
		switch submissionStatus {
		case domain.SubmissionHumanReviewRequired,
			domain.SubmissionInfoRequested:
			return true
		}
	}

	return false
}

func shouldApplySubmissionApproval(
	action string,
	submissionStatus domain.SubmissionStatus,
) bool {
	return submissionStatus == domain.SubmissionHumanReviewRequired &&
		canReconcileApprovalAction(action, submissionStatus)
}

func shouldPublishApprovalPayment(
	submissionStatus domain.SubmissionStatus,
) bool {
	switch submissionStatus {
	case domain.SubmissionHumanReviewRequired,
		domain.SubmissionApprovedByHuman:
		return true
	default:
		return false
	}
}

func approvalEffectEventID(
	item domain.ApprovalItem,
	effect string,
) string {
	version := item.UpdatedAtUTC.UTC().Format("20060102T150405000000000")
	return effect + "_" + item.TrackingID + "_" + version
}

func (s *server) saveApprovalDecisionOrReconcileConflict(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
	action string,
	item domain.ApprovalItem,
	etag string,
) (
	domain.ApprovalItem,
	domain.SubmissionRecord,
	bool,
) {
	err := s.dapr.SaveStateWithETag(
		r.Context(),
		stateStore,
		approvalKey(trackingID),
		item,
		etag,
	)
	if err == nil {
		record, loadErr := s.loadSubmissionRecord(r, trackingID)
		if loadErr != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to reload submission after approval save",
			)
			return domain.ApprovalItem{}, domain.SubmissionRecord{}, false
		}

		return item, record, true
	}

	if !errors.Is(err, daprclient.ErrStateConflict) {
		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to save approval decision",
		)
		return domain.ApprovalItem{}, domain.SubmissionRecord{}, false
	}

	s.log.Info("approval state write conflict detected; reconciling winner", logger.Fields{
		"tracking_id":    trackingID,
		"action":         action,
		"correlation_id": item.CorrelationID,
	})

	reloadedItem, reloadedRecord, shouldSaveAgain, _, ok :=
		s.loadApprovalForActionOrWriteError(
			w,
			r,
			trackingID,
			action,
		)
	if !ok {
		return domain.ApprovalItem{}, domain.SubmissionRecord{}, false
	}

	if shouldSaveAgain {
		httpx.WriteError(
			w,
			r,
			http.StatusConflict,
			"approval changed concurrently; retry the action",
		)
		return domain.ApprovalItem{}, domain.SubmissionRecord{}, false
	}

	return reloadedItem, reloadedRecord, true
}

func (s *server) loadApprovalForActionOrWriteError(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
	action string,
) (
	domain.ApprovalItem,
	domain.SubmissionRecord,
	bool,
	string,
	bool,
) {
	desiredStatus, validAction := approvalStatusForAction(action)
	if !validAction {
		httpx.WriteError(
			w,
			r,
			http.StatusBadRequest,
			"unknown approval action",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	var item domain.ApprovalItem
	found, etag, err := s.dapr.GetStateWithETag(
		r.Context(),
		stateStore,
		approvalKey(trackingID),
		&item,
	)
	if err != nil {
		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to load approval",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	if !found {
		httpx.WriteError(
			w,
			r,
			http.StatusNotFound,
			"approval not found",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	record, err := s.loadSubmissionRecord(r, trackingID)
	if err != nil {
		s.log.Error(
			"failed to load submission before approval reconciliation",
			logger.Fields{
				"error":           err.Error(),
				"tracking_id":     trackingID,
				"action":          action,
				"approval_status": item.Status,
				"correlation_id":  item.CorrelationID,
			},
		)

		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to load submission before approval action",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	if item.Status == domain.ApprovalPending {
		if record.Status != domain.SubmissionHumanReviewRequired {
			httpx.WriteError(
				w,
				r,
				http.StatusConflict,
				"approval action rejected because submission is not in HUMAN_REVIEW_REQUIRED state",
			)
			return domain.ApprovalItem{},
				domain.SubmissionRecord{},
				false,
				"",
				false
		}

		return item, record, true, etag, true
	}

	if item.Status != desiredStatus {
		s.log.Info(
			"conflicting approval retry rejected",
			logger.Fields{
				"tracking_id":      trackingID,
				"requested_action": action,
				"approval_status":  item.Status,
				"correlation_id":   item.CorrelationID,
			},
		)

		httpx.WriteError(
			w,
			r,
			http.StatusConflict,
			"approval was already completed with a different action",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	if !canReconcileApprovalAction(action, record.Status) {
		s.log.Info(
			"approval retry rejected for incompatible submission state",
			logger.Fields{
				"tracking_id":       trackingID,
				"requested_action":  action,
				"approval_status":   item.Status,
				"submission_status": record.Status,
				"correlation_id":    item.CorrelationID,
			},
		)

		httpx.WriteError(
			w,
			r,
			http.StatusConflict,
			"stored approval action cannot be reconciled with current submission state",
		)
		return domain.ApprovalItem{},
			domain.SubmissionRecord{},
			false,
			"",
			false
	}

	s.log.Info(
		"retrying stored approval action",
		logger.Fields{
			"tracking_id":       trackingID,
			"action":            action,
			"approval_status":   item.Status,
			"submission_status": record.Status,
			"correlation_id":    item.CorrelationID,
		},
	)

	return item, record, false, etag, true
}
