package main

import (
	"encoding/json"
	"net/http"
	"time"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/config"
	daprclient "approvalflow/internal/platform/dapr"
	"approvalflow/internal/platform/health"
	"approvalflow/internal/platform/httpx"
	"approvalflow/internal/platform/logger"
)

const serviceName = "payment-service"
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
	mux.HandleFunc("/events/payment-requested", srv.handlePaymentRequested)

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
			Topic:      domain.TopicPaymentRequested,
			Route:      "/events/payment-requested",
		},
	}

	httpx.WriteJSON(w, http.StatusOK, subscriptions)
}

func (s *server) handlePaymentRequested(w http.ResponseWriter, r *http.Request) {
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

	var event domain.PaymentRequestedEvent
	if err := json.Unmarshal(envelope.Data, &event); err != nil {
		s.log.Error("failed to decode payment requested event", logger.Fields{
			"error":          err.Error(),
			"cloud_event_id": envelope.ID,
			"correlation_id": httpx.CorrelationIDFromContext(r.Context()),
		})
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid payment event")
		return
	}

	now := time.Now().UTC()
	paymentID := "pay_" + event.TrackingID
	idempotencyKey := "payment:" + event.TrackingID

	payment := domain.PaymentRecord{
		PaymentID:      paymentID,
		TrackingID:     event.TrackingID,
		InvoiceID:      event.InvoiceID,
		AmountUSD:      event.AmountUSD,
		Status:         domain.PaymentSucceeded,
		Reason:         "Simulated payment succeeded.",
		IdempotencyKey: idempotencyKey,
		CorrelationID:  event.CorrelationID,
		CreatedAtUTC:   now,
		UpdatedAtUTC:   now,
	}

	if err := s.dapr.SaveState(r.Context(), stateStore, idempotencyKey, payment); err != nil {
		s.log.Error("failed to save payment record", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save payment")
		return
	}

	update := domain.ApplyPaymentRequest{
		Status:    domain.SubmissionPaid,
		Reason:    "Payment completed successfully.",
		PaymentID: paymentID,
	}

	status, err := s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+event.TrackingID+"/payment",
		update,
		nil,
	)
	if err != nil {
		s.log.Error("failed to update submission after payment", logger.Fields{
			"error":          err.Error(),
			"status":         status,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission")
		return
	}

	s.log.Info("payment succeeded", logger.Fields{
		"payment_id":     paymentID,
		"tracking_id":    event.TrackingID,
		"invoice_id":     event.InvoiceID,
		"amount_usd":     event.AmountUSD,
		"correlation_id": event.CorrelationID,
	})

	w.WriteHeader(http.StatusNoContent)
}
