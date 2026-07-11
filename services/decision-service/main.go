package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

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
}

type autonomyDailyExposure struct {
	Date            string             `json:"date"`
	SubmitterTotals map[string]float64 `json:"submitter_totals"`
	VendorTotals    map[string]float64 `json:"vendor_totals"`
	UpdatedAtUTC    time.Time          `json:"updated_at_utc"`
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

	policyCfg, err := policy.LoadConfigFromEnv()
	if err != nil {
		log.Error("failed to load policy config", logger.Fields{"error": err.Error()})
		os.Exit(1)
	}

	log.Info("policy config loaded", logger.Fields{
		"autonomy_ceiling_usd": policyCfg.AutonomyCeilingUSD,
		"min_confidence":       policyCfg.MinConfidence,
	})

	srv := &server{
		log:  log,
		dapr: daprclient.NewFromEnv(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler(serviceName))
	mux.HandleFunc("/dapr/subscribe", srv.handleDaprSubscribe)
	mux.HandleFunc("/events/submission-received", srv.handleSubmissionReceived)

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

	policyCfg, err := policy.LoadConfigFromEnv()
	if err != nil {
		s.log.Error("failed to reload policy config", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to reload policy config")
		return
	}

	s.log.Info("policy config reloaded for decision", logger.Fields{
		"tracking_id":          event.TrackingID,
		"correlation_id":       event.CorrelationID,
		"autonomy_ceiling_usd": policyCfg.AutonomyCeilingUSD,
		"min_confidence":       policyCfg.MinConfidence,
		"cumulative_enabled":   policyCfg.CumulativeAutonomyEnabled,
	})

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

	decision := policy.Evaluate(record.Request, policyCfg)
	decision.TrackingID = event.TrackingID
	decision.InvoiceID = event.InvoiceID
	decision.CorrelationID = event.CorrelationID
	if agentErr == nil {
		decision.AgentRecommended = agentRecommendation.RecommendedRoute
	}

	var budgetAdjusted bool
	decision, budgetAdjusted = s.applyCumulativeAutonomyBudget(r.Context(), record.Request, decision, event, policyCfg)
	if budgetAdjusted {
		s.log.Info("auto-approval converted to human review by cumulative autonomy budget", logger.Fields{
			"tracking_id":    event.TrackingID,
			"invoice_id":     event.InvoiceID,
			"amount_usd":     decision.AmountUSD,
			"violations":     violationIDs(decision.Violations),
			"correlation_id": event.CorrelationID,
		})
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
			EventID:       decisionAuditEventID(event, "payment_requested_by_decision"),
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
			RevisionNumber:   record.RevisionNumber,
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
			EventID:       decisionAuditEventID(event, "approval_required_published"),
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
		EventID:       decisionAuditEventID(event, "decision_produced"),
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

func (s *server) applyCumulativeAutonomyBudget(ctx context.Context, req domain.SubmissionRequest, decision domain.DecisionResult, event domain.SubmissionReceivedEvent, cfg policy.Config) (domain.DecisionResult, bool) {
	if !cfg.CumulativeAutonomyEnabled || decision.Route != domain.RouteAutoApprove {
		return decision, false
	}

	dateKey := autonomyDateKey(req.Date, decision.DecidedAtUTC)
	stateKey := "autonomy:daily:" + dateKey

	exposure := autonomyDailyExposure{
		Date:            dateKey,
		SubmitterTotals: map[string]float64{},
		VendorTotals:    map[string]float64{},
	}

	found, err := s.dapr.GetState(ctx, stateStore, stateKey, &exposure)
	if err != nil {
		s.log.Error("failed to load cumulative autonomy exposure; failing closed to human review", logger.Fields{
			"error":          err.Error(),
			"state_key":      stateKey,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})

		decision.Route = domain.RouteHumanReview
		decision.Violations = append(decision.Violations, domain.PolicyViolation{
			RuleID:  "AUTONOMY-BUDGET-UNAVAILABLE",
			Message: "Cumulative autonomy exposure state was unavailable, so the item requires human review.",
		})
		decision.Reason = "Human review required because cumulative autonomy exposure could not be verified."
		return decision, true
	}

	if !found {
		exposure.Date = dateKey
	}
	if exposure.SubmitterTotals == nil {
		exposure.SubmitterTotals = map[string]float64{}
	}
	if exposure.VendorTotals == nil {
		exposure.VendorTotals = map[string]float64{}
	}

	submitterKey := normalizeExposureKey(req.Submitter)
	vendorKey := normalizeExposureKey(req.Vendor)

	budgetContext := policy.AutonomyBudgetContext{
		SubmitterApprovedTodayUSD: exposure.SubmitterTotals[submitterKey],
		VendorApprovedTodayUSD:    exposure.VendorTotals[vendorKey],
	}

	adjusted := policy.ApplyCumulativeAutonomyBudget(decision, cfg, budgetContext)
	if adjusted.Route != domain.RouteAutoApprove {
		return adjusted, true
	}

	exposure.SubmitterTotals[submitterKey] += adjusted.AmountUSD
	exposure.VendorTotals[vendorKey] += adjusted.AmountUSD
	exposure.UpdatedAtUTC = time.Now().UTC()

	if err := s.dapr.SaveState(ctx, stateStore, stateKey, exposure); err != nil {
		s.log.Error("failed to save cumulative autonomy exposure after auto-approval", logger.Fields{
			"error":          err.Error(),
			"state_key":      stateKey,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})

		adjusted.Route = domain.RouteHumanReview
		adjusted.Violations = append(adjusted.Violations, domain.PolicyViolation{
			RuleID:  "AUTONOMY-BUDGET-UNAVAILABLE",
			Message: "Cumulative autonomy exposure could not be persisted, so the item requires human review.",
		})
		adjusted.Reason = "Human review required because cumulative autonomy exposure could not be persisted."
		return adjusted, true
	}

	return adjusted, false
}

func autonomyDateKey(invoiceDate string, fallback time.Time) string {
	invoiceDate = strings.TrimSpace(invoiceDate)
	if len(invoiceDate) >= 10 {
		return invoiceDate[:10]
	}
	return fallback.UTC().Format("2006-01-02")
}

func normalizeExposureKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "unknown"
	}
	return value
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
		return domain.SubmissionHumanReviewRequired
	}
}

func violationIDs(violations []domain.PolicyViolation) []string {
	out := make([]string, 0, len(violations))
	for _, violation := range violations {
		out = append(out, violation.RuleID)
	}
	return out
}

func decisionAuditEventID(
	event domain.SubmissionReceivedEvent,
	action string,
) string {
	return "audit_" + event.TrackingID + "_" + action + "_" + event.EventID
}
