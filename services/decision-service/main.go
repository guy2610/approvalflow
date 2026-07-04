package main

import (
	"encoding/json"
	"net/http"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
	"approvalflow/internal/policy"
)

const serviceName = "decision-service"
const stateStore = "statestore"

type server struct {
	log  *logger.Logger
	dapr *daprclient.Client
	cfg  policy.Config
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
		cfg:  policy.ConfigFromEnv(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))
	mux.HandleFunc("/dapr/subscribe", srv.handleDaprSubscribe)
	mux.HandleFunc("/events/submission-received", srv.handleSubmissionReceived)

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
			Topic:      domain.TopicSubmissionReceived,
			Route:      "/events/submission-received",
		},
	}

	httpx.WriteJSON(w, http.StatusOK, subscriptions)
}

func (s *server) handleSubmissionReceived(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	defer r.Body.Close()

	var envelope daprCloudEvent
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		s.log.Error("failed to decode cloud event", logger.Fields{
			"error":          err.Error(),
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid cloud event")
		return
	}

	var event domain.SubmissionReceivedEvent
	if err := json.Unmarshal(envelope.Data, &event); err != nil {
		s.log.Error("failed to decode submission event", logger.Fields{
			"error":          err.Error(),
			"cloud_event_id": envelope.ID,
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid submission event")
		return
	}

	var record domain.SubmissionRecord
	status, err := s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+event.TrackingID,
		nil,
		&record,
	)
	if err != nil {
		s.log.Error("failed to load submission for decision", logger.Fields{
			"error":          err.Error(),
			"status":         status,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to load submission")
		return
	}

	decision := policy.Evaluate(record.Request, s.cfg)
	decision.TrackingID = event.TrackingID
	decision.InvoiceID = event.InvoiceID
	decision.CorrelationID = event.CorrelationID

	applyDecision := domain.ApplyDecisionRequest{
		Status:        statusFromDecision(decision.Route),
		Reason:        decision.Reason,
		DecisionRoute: decision.Route,
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, "decision:"+event.TrackingID, decision); err != nil {
		s.log.Error("failed to save decision", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save decision")
		return
	}

	status, err = s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+event.TrackingID+"/decision",
		applyDecision,
		nil,
	)
	if err != nil {
		s.log.Error("failed to update submission after decision", logger.Fields{
			"error":          err.Error(),
			"status":         status,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission")
		return
	}

	s.log.Info("decision produced", logger.Fields{
		"event_id":       event.EventID,
		"tracking_id":    event.TrackingID,
		"invoice_id":     event.InvoiceID,
		"route":          decision.Route,
		"amount_usd":     decision.AmountUSD,
		"confidence":     decision.Confidence,
		"violations":     violationIDs(decision.Violations),
		"correlation_id": event.CorrelationID,
	})

	w.WriteHeader(http.StatusNoContent)
}

func statusFromDecision(route domain.DecisionRoute) domain.SubmissionStatus {
	switch route {
	case domain.RouteAutoApprove:
		return domain.SubmissionAutoApprovedPendingPay
	case domain.RouteHumanReview:
		return domain.SubmissionHumanReviewRequired
	case domain.RouteReject:
		return domain.SubmissionRejected
	default:
		return domain.SubmissionProcessing
	}
}

func violationIDs(violations []domain.PolicyViolation) []string {
	out := make([]string, 0, len(violations))
	for _, violation := range violations {
		out = append(out, violation.RuleID)
	}
	return out
}
