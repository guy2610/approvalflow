package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"approvalflow/internal/domain"
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

	if err := http.ListenAndServe(addr, handler); err != nil {
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
		s.log.Info("duplicate approval required event ignored", logger.Fields{
			"tracking_id":    event.TrackingID,
			"invoice_id":     event.InvoiceID,
			"correlation_id": event.CorrelationID,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	now := time.Now().UTC()
	item := domain.ApprovalItem{
		TrackingID:    event.TrackingID,
		InvoiceID:     event.InvoiceID,
		AmountUSD:     event.AmountUSD,
		Status:        domain.ApprovalPending,
		Reason:        event.Reason,
		Violations:    event.Violations,
		CorrelationID: event.CorrelationID,
		CreatedAtUTC:  now,
		UpdatedAtUTC:  now,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid approval action payload")
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

func (s *server) approve(w http.ResponseWriter, r *http.Request, trackingID string, req domain.ApprovalActionRequest) {
	item, ok := s.loadPendingApprovalOrWriteError(w, r, trackingID)
	if !ok {
		return
	}

	item.Status = domain.ApprovalApproved
	item.ActionBy = req.Actor
	item.ActionReason = req.Reason
	item.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, approvalKey(trackingID), item); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save approval")
		return
	}

	if err := s.applySubmissionApproval(r, trackingID, domain.SubmissionApprovedByHuman, "Human approver approved the submission.", req.Actor); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission approval")
		return
	}

	paymentEvent := domain.PaymentRequestedEvent{
		EventID:       "evt_approval_payment_" + trackingID,
		EventType:     domain.TopicPaymentRequested,
		CorrelationID: item.CorrelationID,
		TrackingID:    trackingID,
		InvoiceID:     item.InvoiceID,
		AmountUSD:     item.AmountUSD,
		OccurredAtUTC: time.Now().UTC(),
	}

	if err := s.dapr.PublishEvent(r.Context(), domain.PubSubName, domain.TopicPaymentRequested, paymentEvent); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to publish payment request")
		return
	}

	s.log.Info("approval accepted and payment requested", logger.Fields{
		"tracking_id":    trackingID,
		"invoice_id":     item.InvoiceID,
		"amount_usd":     item.AmountUSD,
		"actor":          req.Actor,
		"correlation_id": item.CorrelationID,
	})

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) reject(w http.ResponseWriter, r *http.Request, trackingID string, req domain.ApprovalActionRequest) {
	item, ok := s.loadPendingApprovalOrWriteError(w, r, trackingID)
	if !ok {
		return
	}

	item.Status = domain.ApprovalRejected
	item.ActionBy = req.Actor
	item.ActionReason = req.Reason
	item.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, approvalKey(trackingID), item); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save rejection")
		return
	}

	reason := "Human approver rejected the submission."
	if req.Reason != "" {
		reason = reason + " Reason: " + req.Reason
	}

	if err := s.applySubmissionApproval(r, trackingID, domain.SubmissionRejectedByHuman, reason, req.Actor); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission rejection")
		return
	}

	s.log.Info("approval rejected", logger.Fields{
		"tracking_id":    trackingID,
		"invoice_id":     item.InvoiceID,
		"actor":          req.Actor,
		"correlation_id": item.CorrelationID,
	})

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) requestInfo(w http.ResponseWriter, r *http.Request, trackingID string, req domain.ApprovalActionRequest) {
	item, ok := s.loadPendingApprovalOrWriteError(w, r, trackingID)
	if !ok {
		return
	}

	item.Status = domain.ApprovalRequestInfo
	item.ActionBy = req.Actor
	item.ActionReason = req.Reason
	item.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(r.Context(), stateStore, approvalKey(trackingID), item); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save request-info")
		return
	}

	reason := "Human approver requested more information."
	if req.Reason != "" {
		reason = reason + " Reason: " + req.Reason
	}

	if err := s.applySubmissionApproval(r, trackingID, domain.SubmissionInfoRequested, reason, req.Actor); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission request-info")
		return
	}

	s.log.Info("approval requested more info", logger.Fields{
		"tracking_id":    trackingID,
		"invoice_id":     item.InvoiceID,
		"actor":          req.Actor,
		"correlation_id": item.CorrelationID,
	})

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *server) loadPendingApprovalOrWriteError(w http.ResponseWriter, r *http.Request, trackingID string) (domain.ApprovalItem, bool) {
	var item domain.ApprovalItem
	found, err := s.dapr.GetState(r.Context(), stateStore, approvalKey(trackingID), &item)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load approval")
		return domain.ApprovalItem{}, false
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "approval not found")
		return domain.ApprovalItem{}, false
	}

	if item.Status != domain.ApprovalPending {
		httpx.WriteError(w, r, http.StatusConflict, "approval is no longer pending")
		return domain.ApprovalItem{}, false
	}

	return item, true
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
