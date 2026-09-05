package main

import (
	"context"
	"encoding/json"
	"errors"
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
	Reservations    map[string]autonomyReservation `json:"reservations"`
	Date            string                         `json:"date"`
	SubmitterTotals map[string]float64             `json:"submitter_totals"`
	VendorTotals    map[string]float64             `json:"vendor_totals"`
	UpdatedAtUTC    time.Time                      `json:"updated_at_utc"`
}

// Reservations and totals are committed together in the daily aggregate.
type autonomyReservation struct {
	ReservationID  string                   `json:"reservation_id"`
	TrackingID     string                   `json:"tracking_id"`
	RevisionNumber int                      `json:"revision_number"`
	SourceEventID  string                   `json:"source_event_id"`
	SubmitterKey   string                   `json:"submitter_key"`
	VendorKey      string                   `json:"vendor_key"`
	AmountUSD      float64                  `json:"amount_usd"`
	Admitted       bool                     `json:"admitted"`
	Reason         string                   `json:"reason"`
	Violations     []domain.PolicyViolation `json:"violations"`
	CreatedAtUTC   time.Time                `json:"created_at_utc"`
}

const autonomyReservationAttempts = 5

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

	if strings.TrimSpace(event.TrackingID) == "" || event.RevisionNumber <= 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "tracking ID and positive event revision are required")
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

	if event.RevisionNumber < record.RevisionNumber {
		s.log.Info("stale submission event acknowledged", logger.Fields{
			"tracking_id": event.TrackingID, "event_revision": event.RevisionNumber,
			"current_revision": record.RevisionNumber, "source_event_id": event.EventID,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if event.RevisionNumber > record.RevisionNumber {
		httpx.WriteError(w, r, http.StatusInternalServerError, "event revision is ahead of submission state")
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
	decision, budgetAdjusted, err = s.applyCumulativeAutonomyBudget(r.Context(), record.Request, decision, event, policyCfg)
	if err != nil {
		s.log.Error("failed to reserve cumulative autonomy exposure", logger.Fields{"error": err.Error(), "tracking_id": event.TrackingID})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to reserve cumulative autonomy exposure")
		return
	}
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

func (s *server) applyCumulativeAutonomyBudget(ctx context.Context, req domain.SubmissionRequest, decision domain.DecisionResult, event domain.SubmissionReceivedEvent, cfg policy.Config) (domain.DecisionResult, bool, error) {
	if strings.TrimSpace(event.TrackingID) == "" || event.RevisionNumber <= 0 {
		return decision, false, fmt.Errorf("invalid autonomy reservation identity")
	}
	dateKey := autonomyDateKey(req.Date, decision.DecidedAtUTC)
	stateKey := "autonomy:daily:" + dateKey
	reservationID := fmt.Sprintf("%s:revision:%d", event.TrackingID, event.RevisionNumber)
	var exposure autonomyDailyExposure
	found, etag, err := s.dapr.GetStateStrongWithETag(ctx, stateStore, stateKey, &exposure)
	if err != nil {
		return decision, false, err
	}
	for attempt := 0; attempt <= autonomyReservationAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return decision, false, err
		}
		if exposure.SubmitterTotals == nil {
			exposure.SubmitterTotals = map[string]float64{}
		}
		if exposure.VendorTotals == nil {
			exposure.VendorTotals = map[string]float64{}
		}
		if exposure.Reservations == nil {
			exposure.Reservations = map[string]autonomyReservation{}
		}
		if saved, ok := exposure.Reservations[reservationID]; ok {
			decision.Route = domain.RouteHumanReview
			if saved.Admitted {
				decision.Route = domain.RouteAutoApprove
			}
			decision.AmountUSD = saved.AmountUSD
			decision.Reason = saved.Reason
			decision.Violations = saved.Violations
			return decision, !saved.Admitted, nil
		}
		// The final reread may reveal our committed reservation, but cannot start another write.
		if attempt == autonomyReservationAttempts {
			break
		}
		// Stored outcomes remain authoritative even after a config or router change.
		if !cfg.CumulativeAutonomyEnabled || decision.Route != domain.RouteAutoApprove {
			return decision, false, nil
		}
		if !found {
			exposure.Date = dateKey
		}
		submitterKey, vendorKey := normalizeExposureKey(req.Submitter), normalizeExposureKey(req.Vendor)
		adjusted := policy.ApplyCumulativeAutonomyBudget(decision, cfg, policy.AutonomyBudgetContext{
			SubmitterApprovedTodayUSD: exposure.SubmitterTotals[submitterKey],
			VendorApprovedTodayUSD:    exposure.VendorTotals[vendorKey],
		})
		admitted := adjusted.Route == domain.RouteAutoApprove
		now := time.Now().UTC()
		exposure.Reservations[reservationID] = autonomyReservation{
			ReservationID: reservationID, TrackingID: event.TrackingID, RevisionNumber: event.RevisionNumber,
			SourceEventID: event.EventID, SubmitterKey: submitterKey, VendorKey: vendorKey,
			AmountUSD: adjusted.AmountUSD, Admitted: admitted, Reason: adjusted.Reason,
			Violations: adjusted.Violations, CreatedAtUTC: now,
		}
		if admitted {
			exposure.SubmitterTotals[submitterKey] += adjusted.AmountUSD
			exposure.VendorTotals[vendorKey] += adjusted.AmountUSD
		}
		exposure.UpdatedAtUTC = now
		if found {
			err = s.dapr.SaveStateWithETag(ctx, stateStore, stateKey, exposure, etag)
			if err == nil {
				return adjusted, !admitted, nil
			}
			if !errors.Is(err, daprclient.ErrStateConflict) {
				return decision, false, err
			}
			// Discard all local increments before reading after a CAS conflict.
			exposure = autonomyDailyExposure{}
			found, etag, err = s.dapr.GetStateStrongWithETag(ctx, stateStore, stateKey, &exposure)
		} else {
			err = s.dapr.SaveStateTransaction(ctx, stateStore, []daprclient.StateTransactionItem{{Key: stateKey, Value: exposure, CreateOnly: true}})
			if err == nil {
				return adjusted, !admitted, nil
			}
			// Dapr 1.14.4 transaction errors are ambiguous, including HTTP 500.
			// Reconcile from authoritative state without classifying infrastructure errors as conflicts.
			exposure, etag, err = s.reconcileAutonomyCreation(ctx, stateKey, err)
			found = err == nil
		}
		if err != nil {
			return decision, false, err
		}
	}
	return decision, false, fmt.Errorf("cumulative autonomy reservation retries exhausted")
}

const autonomyCreationReadAttempts = 3

func (s *server) reconcileAutonomyCreation(ctx context.Context, key string, createErr error) (autonomyDailyExposure, string, error) {
	var readErr error
	for attempt := 0; attempt < autonomyCreationReadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return autonomyDailyExposure{}, "", errors.Join(createErr, err)
		}
		var exposure autonomyDailyExposure
		found, etag, err := s.dapr.GetStateStrongWithETag(ctx, stateStore, key, &exposure)
		if err == nil && found {
			return exposure, etag, nil
		}
		if err != nil {
			readErr = err
		}
	}
	return autonomyDailyExposure{}, "", fmt.Errorf("autonomy creation could not be reconciled: %w", errors.Join(createErr, readErr))
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
