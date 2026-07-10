package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"approvalflow/internal/domain"
	"approvalflow/internal/platform/httpx"
)

func notificationFromAuditEvent(event domain.AuditEvent) (domain.Notification, bool) {
	status, message, ok := notificationOutcome(event)
	if !ok || event.TrackingID == "" {
		return domain.Notification{}, false
	}

	createdAt := event.OccurredAtUTC
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	return domain.Notification{
		NotificationID: "notification_" + event.TrackingID,
		TrackingID:     event.TrackingID,
		InvoiceID:      event.InvoiceID,
		CorrelationID:  event.CorrelationID,
		Status:         status,
		Message:        message,
		Acknowledged:   false,
		CreatedAtUTC:   createdAt,
	}, true
}

func notificationOutcome(
	event domain.AuditEvent,
) (domain.SubmissionStatus, string, bool) {
	switch event.Action {
	case "payment_succeeded":
		return domain.SubmissionPaid,
			"Invoice payment completed successfully.",
			true

	case "payment_failed_compensated":
		return domain.SubmissionPaymentFailed,
			"Invoice payment failed; compensation state was preserved.",
			true

	case "human_rejected":
		return domain.SubmissionRejectedByHuman,
			"Invoice was rejected by a human approver.",
			true

	case "decision_produced":
		route := analyticsStringField(event, "route")
		if route == "" {
			route = event.Outcome
		}

		if route == string(domain.RouteReject) {
			return domain.SubmissionRejected,
				"Invoice was rejected by deterministic policy evaluation.",
				true
		}
	}

	return "", "", false
}

func (s *server) persistNotificationForAuditEvent(
	r *http.Request,
	event domain.AuditEvent,
) error {
	notification, ok := notificationFromAuditEvent(event)
	if !ok {
		return nil
	}

	key := notificationStateKey(notification.TrackingID)

	var existing domain.Notification
	found, err := s.dapr.GetState(
		r.Context(),
		stateStore,
		key,
		&existing,
	)
	if err != nil {
		return fmt.Errorf("load notification state: %w", err)
	}

	// A terminal notification is immutable except for acknowledgement state.
	// Re-delivered audit events must not reset an acknowledgement.
	if found {
		notification.Acknowledged = existing.Acknowledged
		notification.AcknowledgedAtUTC = existing.AcknowledgedAtUTC
		if !existing.CreatedAtUTC.IsZero() {
			notification.CreatedAtUTC = existing.CreatedAtUTC
		}
	}

	if err := s.dapr.SaveState(
		r.Context(),
		stateStore,
		key,
		notification,
	); err != nil {
		return fmt.Errorf("save notification state: %w", err)
	}

	return nil
}

func (s *server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/notifications/")
	path = strings.Trim(path, "/")
	if path == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "missing tracking id")
		return
	}

	if strings.HasSuffix(path, "/acknowledge") {
		trackingID := strings.TrimSuffix(path, "/acknowledge")
		trackingID = strings.Trim(trackingID, "/")
		if trackingID == "" {
			httpx.WriteError(w, r, http.StatusBadRequest, "missing tracking id")
			return
		}

		if r.Method != http.MethodPost {
			httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		s.acknowledgeNotification(w, r, trackingID)
		return
	}

	if r.Method != http.MethodGet {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.getNotification(w, r, path)
}

func (s *server) getNotification(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
) {
	var notification domain.Notification
	found, err := s.dapr.GetState(
		r.Context(),
		stateStore,
		notificationStateKey(trackingID),
		&notification,
	)
	if err != nil {
		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to load notification",
		)
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "notification not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, notification)
}

func (s *server) acknowledgeNotification(
	w http.ResponseWriter,
	r *http.Request,
	trackingID string,
) {
	var notification domain.Notification
	found, err := s.dapr.GetState(
		r.Context(),
		stateStore,
		notificationStateKey(trackingID),
		&notification,
	)
	if err != nil {
		httpx.WriteError(
			w,
			r,
			http.StatusInternalServerError,
			"failed to load notification",
		)
		return
	}

	if !found {
		httpx.WriteError(w, r, http.StatusNotFound, "notification not found")
		return
	}

	if !notification.Acknowledged {
		now := time.Now().UTC()
		notification.Acknowledged = true
		notification.AcknowledgedAtUTC = &now

		if err := s.dapr.SaveState(
			r.Context(),
			stateStore,
			notificationStateKey(trackingID),
			notification,
		); err != nil {
			httpx.WriteError(
				w,
				r,
				http.StatusInternalServerError,
				"failed to acknowledge notification",
			)
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, notification)
}

func notificationStateKey(trackingID string) string {
	return "notification:" + trackingID
}
