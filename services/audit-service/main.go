package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

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
	mux.HandleFunc("/events/audit", srv.handleAuditEvent)
	mux.HandleFunc("/audit/", srv.handleAuditTrail)

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
