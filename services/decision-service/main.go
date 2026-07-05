package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/auditlog"
	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
	"approvalflow/internal/policy"
)

const serviceName = "decision-service"
const stateStore = "statestore"
const agentServiceAppID = "agent-service"

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

	agentRecommendation, agentErr := s.askAgent(r, event, record)
	if agentErr != nil {
		s.log.Error("agent recommendation unavailable; continuing with deterministic router", logger.Fields{
			"error":          agentErr.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
	}

	decision := policy.Evaluate(record.Request, s.cfg)
	decision.TrackingID = event.TrackingID
	decision.InvoiceID = event.InvoiceID
	decision.CorrelationID = event.CorrelationID
	if agentErr == nil {
		decision.AgentRecommended = agentRecommendation.RecommendedRoute
	}

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

	if decision.Route == domain.RouteAutoApprove {
		paymentEvent := domain.PaymentRequestedEvent{
			EventID:       "evt_" + event.EventID,
			EventType:     domain.TopicPaymentRequested,
			CorrelationID: event.CorrelationID,
			TrackingID:    event.TrackingID,
			InvoiceID:     event.InvoiceID,
			AmountUSD:     decision.AmountUSD,
			OccurredAtUTC: decision.DecidedAtUTC,
		}

		if err := s.dapr.PublishEvent(r.Context(), domain.PubSubName, domain.TopicPaymentRequested, paymentEvent); err != nil {
			s.log.Error("failed to publish payment requested event", logger.Fields{
				"error":          err.Error(),
				"tracking_id":    event.TrackingID,
				"correlation_id": event.CorrelationID,
			})
			httpx.WriteError(w, r, http.StatusInternalServerError, "failed to publish payment requested event")
			return
		}

		s.log.Info("payment requested event published", logger.Fields{
			"event_id":       paymentEvent.EventID,
			"tracking_id":    event.TrackingID,
			"invoice_id":     event.InvoiceID,
			"amount_usd":     decision.AmountUSD,
			"correlation_id": event.CorrelationID,
		})

		if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
			EventID:       "audit_" + event.TrackingID + "_payment_requested_by_decision",
			CorrelationID: event.CorrelationID,
			TrackingID:    event.TrackingID,
			InvoiceID:     event.InvoiceID,
			Service:       serviceName,
			Action:        "payment_requested_published",
			Outcome:       "published",
			Reason:        decision.Reason,
			Fields: map[string]any{
				"route":      decision.Route,
				"amount_usd": decision.AmountUSD,
			},
		}); err != nil {
			s.log.Error("failed to publish audit event", logger.Fields{
				"error":          err.Error(),
				"tracking_id":    event.TrackingID,
				"correlation_id": event.CorrelationID,
			})
		}
	}

	if decision.Route == domain.RouteHumanReview {
		approvalEvent := domain.ApprovalRequiredEvent{
			EventID:          "evt_" + event.EventID,
			EventType:        domain.TopicApprovalRequired,
			CorrelationID:    event.CorrelationID,
			TrackingID:       event.TrackingID,
			InvoiceID:        event.InvoiceID,
			AmountUSD:        decision.AmountUSD,
			Reason:           decision.Reason,
			Violations:       violationIDs(decision.Violations),
			AgentRecommended: string(agentRecommendation.RecommendedRoute),
			AgentConfidence:  agentRecommendation.Confidence,
			AgentCitedRules:  citedRuleIDs(agentRecommendation.CitedRules),
			OccurredAtUTC:    decision.DecidedAtUTC,
		}

		if err := s.dapr.PublishEvent(r.Context(), domain.PubSubName, domain.TopicApprovalRequired, approvalEvent); err != nil {
			s.log.Error("failed to publish approval required event", logger.Fields{
				"error":          err.Error(),
				"tracking_id":    event.TrackingID,
				"correlation_id": event.CorrelationID,
			})
			httpx.WriteError(w, r, http.StatusInternalServerError, "failed to publish approval required event")
			return
		}

		s.log.Info("approval required event published", logger.Fields{
			"event_id":       approvalEvent.EventID,
			"tracking_id":    event.TrackingID,
			"invoice_id":     event.InvoiceID,
			"amount_usd":     decision.AmountUSD,
			"violations":     approvalEvent.Violations,
			"correlation_id": event.CorrelationID,
		})

		if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
			EventID:       "audit_" + event.TrackingID + "_approval_required_published",
			CorrelationID: event.CorrelationID,
			TrackingID:    event.TrackingID,
			InvoiceID:     event.InvoiceID,
			Service:       serviceName,
			Action:        "approval_required_published",
			Outcome:       "published",
			Reason:        decision.Reason,
			Fields: map[string]any{
				"route":             decision.Route,
				"amount_usd":        decision.AmountUSD,
				"violations":        approvalEvent.Violations,
				"agent_recommended": approvalEvent.AgentRecommended,
				"agent_confidence":  approvalEvent.AgentConfidence,
				"agent_cited_rules": approvalEvent.AgentCitedRules,
			},
		}); err != nil {
			s.log.Error("failed to publish audit event", logger.Fields{
				"error":          err.Error(),
				"tracking_id":    event.TrackingID,
				"correlation_id": event.CorrelationID,
			})
		}
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
		"event_id":          event.EventID,
		"tracking_id":       event.TrackingID,
		"invoice_id":        event.InvoiceID,
		"agent_recommended": decision.AgentRecommended,
		"route":             decision.Route,
		"amount_usd":        decision.AmountUSD,
		"confidence":        decision.Confidence,
		"violations":        violationIDs(decision.Violations),
		"correlation_id":    event.CorrelationID,
	})

	if err := auditlog.Publish(r.Context(), s.dapr, auditlog.Event{
		EventID:       "audit_" + event.TrackingID + "_decision_produced",
		CorrelationID: event.CorrelationID,
		TrackingID:    event.TrackingID,
		InvoiceID:     event.InvoiceID,
		Service:       serviceName,
		Action:        "decision_produced",
		Outcome:       string(decision.Route),
		Reason:        decision.Reason,
		Fields: map[string]any{
			"agent_recommended": decision.AgentRecommended,
			"route":             decision.Route,
			"amount_usd":        decision.AmountUSD,
			"router_confidence": decision.Confidence,
			"violations":        violationIDs(decision.Violations),
		},
	}); err != nil {
		s.log.Error("failed to publish audit event", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) askAgent(r *http.Request, event domain.SubmissionReceivedEvent, record domain.SubmissionRecord) (domain.AgentRecommendation, error) {
	request := domain.AgentRecommendationRequest{
		TrackingID:    event.TrackingID,
		CorrelationID: event.CorrelationID,
		Submission:    record.Request,
	}

	var recommendation domain.AgentRecommendation
	status, err := s.dapr.InvokeJSON(
		r.Context(),
		agentServiceAppID,
		"recommend",
		request,
		&recommendation,
	)
	if err != nil {
		return domain.AgentRecommendation{}, err
	}

	if status < 200 || status >= 300 {
		return domain.AgentRecommendation{}, fmt.Errorf("agent returned status %d", status)
	}

	s.log.Info("agent recommendation received", logger.Fields{
		"tracking_id":       event.TrackingID,
		"invoice_id":        event.InvoiceID,
		"recommended_route": recommendation.RecommendedRoute,
		"agent_confidence":  recommendation.Confidence,
		"provider":          recommendation.Provider,
		"cited_rules":       citedRuleIDs(recommendation.CitedRules),
		"correlation_id":    event.CorrelationID,
	})

	return recommendation, nil
}

func citedRuleIDs(rules []domain.CitedRule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.RuleID)
	}
	return out
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
