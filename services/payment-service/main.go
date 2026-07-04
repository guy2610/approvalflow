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

	idempotencyKey := "payment:" + event.TrackingID

	var existing domain.PaymentRecord
	found, err := s.dapr.GetState(r.Context(), stateStore, idempotencyKey, &existing)
	if err != nil {
		s.log.Error("failed to check payment idempotency", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to check payment idempotency")
		return
	}

	if found {
		if err := s.applyPaymentStatus(r, event.TrackingID, existing.PaymentID, existing.Status, duplicatePaymentReason(existing.Status)); err != nil {
			s.log.Error("failed to re-apply payment status for duplicate payment request", logger.Fields{
				"error":          err.Error(),
				"payment_id":     existing.PaymentID,
				"tracking_id":    event.TrackingID,
				"correlation_id": event.CorrelationID,
			})
			httpx.WriteError(w, r, http.StatusInternalServerError, "failed to re-apply payment status")
			return
		}

		s.log.Info("duplicate payment request ignored", logger.Fields{
			"payment_id":      existing.PaymentID,
			"payment_status":  existing.Status,
			"tracking_id":     event.TrackingID,
			"invoice_id":      event.InvoiceID,
			"amount_usd":      existing.AmountUSD,
			"idempotency_key": idempotencyKey,
			"correlation_id":  event.CorrelationID,
		})

		w.WriteHeader(http.StatusNoContent)
		return
	}

	payment := s.simulatePayment(event, idempotencyKey)

	if err := s.dapr.SaveState(r.Context(), stateStore, idempotencyKey, payment); err != nil {
		s.log.Error("failed to save payment record", logger.Fields{
			"error":          err.Error(),
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to save payment")
		return
	}

	if err := s.applyPaymentStatus(r, event.TrackingID, payment.PaymentID, payment.Status, payment.Reason); err != nil {
		s.log.Error("failed to update submission after payment", logger.Fields{
			"error":          err.Error(),
			"payment_id":     payment.PaymentID,
			"tracking_id":    event.TrackingID,
			"correlation_id": event.CorrelationID,
		})
		httpx.WriteError(w, r, http.StatusInternalServerError, "failed to update submission")
		return
	}

	if payment.Status == domain.PaymentFailed {
		s.log.Error("payment failed and compensation applied", logger.Fields{
			"payment_id":     payment.PaymentID,
			"tracking_id":    event.TrackingID,
			"invoice_id":     event.InvoiceID,
			"amount_usd":     event.AmountUSD,
			"correlation_id": event.CorrelationID,
			"reason":         payment.Reason,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.log.Info("payment succeeded", logger.Fields{
		"payment_id":     payment.PaymentID,
		"tracking_id":    event.TrackingID,
		"invoice_id":     event.InvoiceID,
		"amount_usd":     event.AmountUSD,
		"correlation_id": event.CorrelationID,
	})

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) simulatePayment(event domain.PaymentRequestedEvent, idempotencyKey string) domain.PaymentRecord {
	now := time.Now().UTC()
	paymentID := "pay_" + event.TrackingID

	status := domain.PaymentSucceeded
	reason := "Simulated payment succeeded."

	if shouldSimulatePaymentFailure(event) {
		status = domain.PaymentFailed
		reason = "Simulated payment processor failure; compensation applied by marking submission as PAYMENT_FAILED."
	}

	return domain.PaymentRecord{
		PaymentID:      paymentID,
		TrackingID:     event.TrackingID,
		InvoiceID:      event.InvoiceID,
		AmountUSD:      event.AmountUSD,
		Status:         status,
		Reason:         reason,
		IdempotencyKey: idempotencyKey,
		CorrelationID:  event.CorrelationID,
		CreatedAtUTC:   now,
		UpdatedAtUTC:   now,
	}
}

func shouldSimulatePaymentFailure(event domain.PaymentRequestedEvent) bool {
	return event.InvoiceID == "INV-1012"
}

func (s *server) applyPaymentStatus(r *http.Request, trackingID string, paymentID string, paymentStatus domain.PaymentStatus, reason string) error {
	update := domain.ApplyPaymentRequest{
		Status:    submissionStatusFromPayment(paymentStatus),
		Reason:    reason,
		PaymentID: paymentID,
	}

	_, err := s.dapr.InvokeJSON(
		r.Context(),
		"submission-service",
		"internal/submissions/"+trackingID+"/payment",
		update,
		nil,
	)

	return err
}

func submissionStatusFromPayment(status domain.PaymentStatus) domain.SubmissionStatus {
	switch status {
	case domain.PaymentSucceeded:
		return domain.SubmissionPaid
	case domain.PaymentFailed:
		return domain.SubmissionPaymentFailed
	default:
		return domain.SubmissionPaymentPending
	}
}

func duplicatePaymentReason(status domain.PaymentStatus) string {
	switch status {
	case domain.PaymentSucceeded:
		return "Payment already completed; duplicate payment request ignored."
	case domain.PaymentFailed:
		return "Payment had already failed; duplicate payment request ignored and compensation state preserved."
	default:
		return "Duplicate payment request ignored."
	}
}
