package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
)

const serviceName = "audit-service"
const stateStore = "statestore"

type server struct {
	log     *logger.Logger
	dapr    *daprclient.Client
	auditMu sync.Mutex
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
	mux.HandleFunc("/events/audit", srv.handleAuditEvent)
	mux.HandleFunc("/audit/", srv.handleAuditTrail)
	mux.HandleFunc("/analytics/summary", srv.handleAnalyticsSummary)

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

	httpx.WriteJSON(w, http.StatusOK, []daprSubscription{
		{
			PubSubName: domain.PubSubName,
			Topic:      domain.TopicAuditEvent,
			Route:      "/events/audit",
		},
	})
}

func (s *server) handleAuditEvent(w http.ResponseWriter, r *http.Request) {
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

	var event domain.AuditEvent
	if err := json.Unmarshal(envelope.Data, &event); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid audit event")
		return
	}

	if event.EventID == "" || event.CorrelationID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing audit event id or correlation id")
		return
	}

	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	eventKey := auditEventKey(event.EventID)
	indexKey := auditIndexKey(event.CorrelationID)

	var existing domain.AuditEvent
	found, err := s.dapr.GetState(r.Context(), stateStore, eventKey, &existing)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to check audit event")
		return
	}

	if found {
		s.log.Info("duplicate audit event ignored", logger.Fields{
			"event_id":       event.EventID,
			"correlation_id": event.CorrelationID,
			"service":        event.Service,
			"action":         event.Action,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var analytics analyticsState
	analyticsFound, err := s.dapr.GetState(
		r.Context(),
		stateStore,
		analyticsStateKey(),
		&analytics,
	)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load analytics state")
		return
	}
	if !analyticsFound {
		analytics = newAnalyticsState()
	}

	analytics = applyAuditEventToAnalytics(analytics, event)
	if err := s.dapr.SaveState(
		r.Context(),
		stateStore,
		analyticsStateKey(),
		analytics,
	); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update analytics state")
		return
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, eventKey, event); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save audit event")
		return
	}

	ids, err := s.loadAuditIndex(r, indexKey)
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load audit index")
		return
	}

	ids = appendIfMissing(ids, event.EventID)

	if err := s.dapr.SaveState(r.Context(), stateStore, indexKey, ids); err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save audit index")
		return
	}

	s.log.Info("audit event stored", logger.Fields{
		"event_id":       event.EventID,
		"correlation_id": event.CorrelationID,
		"tracking_id":    event.TrackingID,
		"invoice_id":     event.InvoiceID,
		"service":        event.Service,
		"action":         event.Action,
		"outcome":        event.Outcome,
	})

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAuditTrail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	correlationID := strings.TrimPrefix(r.URL.Path, "/audit/")
	correlationID = strings.Trim(correlationID, "/")
	if correlationID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing correlation id")
		return
	}

	ids, err := s.loadAuditIndex(r, auditIndexKey(correlationID))
	if err != nil {
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load audit trail")
		return
	}

	events := make([]domain.AuditEvent, 0, len(ids))
	for _, id := range ids {
		var event domain.AuditEvent
		found, err := s.dapr.GetState(r.Context(), stateStore, auditEventKey(id), &event)
		if err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load audit event")
			return
		}
		if found {
			events = append(events, event)
		}
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAtUTC.Before(events[j].OccurredAtUTC)
	})

	httpx.WriteJSON(w, http.StatusOK, domain.AuditTrailResponse{
		CorrelationID: correlationID,
		Events:        events,
	})
}

func (s *server) loadAuditIndex(r *http.Request, key string) ([]string, error) {
	var ids []string
	found, err := s.dapr.GetState(r.Context(), stateStore, key, &ids)
	if err != nil {
		return nil, err
	}
	if !found {
		return []string{}, nil
	}
	return ids, nil
}

func appendIfMissing(ids []string, candidate string) []string {
	for _, id := range ids {
		if id == candidate {
			return ids
		}
	}
	return append(ids, candidate)
}

func auditEventKey(eventID string) string {
	return "audit:event:" + eventID
}

func auditIndexKey(correlationID string) string {
	return "audit:index:" + correlationID
}

type analyticsState struct {
	Summary                  domain.AnalyticsSummary `json:"summary"`
	ProcessedEventIDs        map[string]bool         `json:"processed_event_ids"`
	SubmissionTrackingIDs    map[string]bool         `json:"submission_tracking_ids"`
	AutoApprovedTrackingIDs  map[string]bool         `json:"auto_approved_tracking_ids"`
	HumanReviewTrackingIDs   map[string]bool         `json:"human_review_tracking_ids"`
	HumanApprovedTrackingIDs map[string]bool         `json:"human_approved_tracking_ids"`
	RejectedTrackingIDs      map[string]bool         `json:"rejected_tracking_ids"`
	PaymentFailedTrackingIDs map[string]bool         `json:"payment_failed_tracking_ids"`
	CompletedTrackingIDs     map[string]bool         `json:"completed_tracking_ids"`
}

func newAnalyticsState() analyticsState {
	return analyticsState{
		ProcessedEventIDs:        map[string]bool{},
		SubmissionTrackingIDs:    map[string]bool{},
		AutoApprovedTrackingIDs:  map[string]bool{},
		HumanReviewTrackingIDs:   map[string]bool{},
		HumanApprovedTrackingIDs: map[string]bool{},
		RejectedTrackingIDs:      map[string]bool{},
		PaymentFailedTrackingIDs: map[string]bool{},
		CompletedTrackingIDs:     map[string]bool{},
	}
}

func normalizeAnalyticsState(state analyticsState) analyticsState {
	if state.ProcessedEventIDs == nil {
		state.ProcessedEventIDs = map[string]bool{}
	}
	if state.SubmissionTrackingIDs == nil {
		state.SubmissionTrackingIDs = map[string]bool{}
	}
	if state.AutoApprovedTrackingIDs == nil {
		state.AutoApprovedTrackingIDs = map[string]bool{}
	}
	if state.HumanReviewTrackingIDs == nil {
		state.HumanReviewTrackingIDs = map[string]bool{}
	}
	if state.HumanApprovedTrackingIDs == nil {
		state.HumanApprovedTrackingIDs = map[string]bool{}
	}
	if state.RejectedTrackingIDs == nil {
		state.RejectedTrackingIDs = map[string]bool{}
	}
	if state.PaymentFailedTrackingIDs == nil {
		state.PaymentFailedTrackingIDs = map[string]bool{}
	}
	if state.CompletedTrackingIDs == nil {
		state.CompletedTrackingIDs = map[string]bool{}
	}
	return state
}

func applyAuditEventToAnalytics(
	state analyticsState,
	event domain.AuditEvent,
) analyticsState {
	state = normalizeAnalyticsState(state)

	if event.EventID == "" || state.ProcessedEventIDs[event.EventID] {
		return state
	}
	state.ProcessedEventIDs[event.EventID] = true

	trackingID := event.TrackingID
	if trackingID == "" {
		return refreshAnalyticsRates(state)
	}

	switch event.Action {
	case "submission_accepted":
		if !state.SubmissionTrackingIDs[trackingID] {
			state.SubmissionTrackingIDs[trackingID] = true
			state.Summary.TotalSubmissions++
		}

	case "decision_produced":
		route := analyticsStringField(event, "route")
		if route == "" {
			route = event.Outcome
		}

		switch route {
		case string(domain.RouteAutoApprove):
			if !state.AutoApprovedTrackingIDs[trackingID] {
				state.AutoApprovedTrackingIDs[trackingID] = true
				state.Summary.AutoApprovedCount++
				state.Summary.AutoApprovedAmountUSD += analyticsNumberField(event, "amount_usd")
			}

		case string(domain.RouteHumanReview):
			if !state.HumanReviewTrackingIDs[trackingID] {
				state.HumanReviewTrackingIDs[trackingID] = true
				state.Summary.HumanReviewCount++
			}

		case string(domain.RouteReject):
			markAnalyticsRejected(&state, trackingID)
		}

	case "human_approved":
		if !state.HumanApprovedTrackingIDs[trackingID] {
			state.HumanApprovedTrackingIDs[trackingID] = true
			state.Summary.HumanApprovedCount++
			state.Summary.HumanApprovedAmountUSD += analyticsNumberField(event, "amount_usd")
		}

	case "human_rejected":
		markAnalyticsRejected(&state, trackingID)

	case "payment_succeeded":
		markAnalyticsCompleted(&state, trackingID)

	case "payment_failed_compensated":
		if !state.PaymentFailedTrackingIDs[trackingID] {
			state.PaymentFailedTrackingIDs[trackingID] = true
			state.Summary.PaymentFailedCount++
		}
		markAnalyticsCompleted(&state, trackingID)
	}

	state.Summary.UpdatedAtUTC = event.OccurredAtUTC
	return refreshAnalyticsRates(state)
}

func markAnalyticsRejected(state *analyticsState, trackingID string) {
	if !state.RejectedTrackingIDs[trackingID] {
		state.RejectedTrackingIDs[trackingID] = true
		state.Summary.RejectedCount++
	}
	markAnalyticsCompleted(state, trackingID)
}

func markAnalyticsCompleted(state *analyticsState, trackingID string) {
	if !state.CompletedTrackingIDs[trackingID] {
		state.CompletedTrackingIDs[trackingID] = true
		state.Summary.CompletedSubmissions++
	}
}

func refreshAnalyticsRates(state analyticsState) analyticsState {
	if state.Summary.TotalSubmissions <= 0 {
		state.Summary.HumanReviewRate = 0
		state.Summary.AutoApprovalRate = 0
		return state
	}

	total := float64(state.Summary.TotalSubmissions)
	state.Summary.HumanReviewRate = float64(state.Summary.HumanReviewCount) / total
	state.Summary.AutoApprovalRate = float64(state.Summary.AutoApprovedCount) / total
	return state
}

func analyticsStringField(event domain.AuditEvent, key string) string {
	if event.Fields == nil {
		return ""
	}

	value, ok := event.Fields[key]
	if !ok {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return typed
	case domain.DecisionRoute:
		return string(typed)
	default:
		return ""
	}
}

func analyticsNumberField(event domain.AuditEvent, key string) float64 {
	if event.Fields == nil {
		return 0
	}

	value, ok := event.Fields[key]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		number, err := typed.Float64()
		if err == nil {
			return number
		}
	}

	return 0
}

func analyticsStateKey() string {
	return "analytics:summary"
}

func (s *server) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var state analyticsState
	found, err := s.dapr.GetState(
		r.Context(),
		stateStore,
		analyticsStateKey(),
		&state,
	)
	if err != nil {
		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to load analytics summary",
		)
		return
	}

	if !found {
		state = newAnalyticsState()
	}

	state = normalizeAnalyticsState(state)
	state = refreshAnalyticsRates(state)

	httpx.WriteJSON(w, http.StatusOK, state.Summary)
}
