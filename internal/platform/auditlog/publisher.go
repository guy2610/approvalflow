package auditlog

import (
	"context"
	"time"

	"approvalflow/internal/domain"
	daprclient "approvalflow/internal/platform/dapr"
)

type Event struct {
	EventID       string
	CorrelationID string
	TrackingID    string
	InvoiceID     string
	Service       string
	Action        string
	Outcome       string
	Reason        string
	Fields        map[string]any
}

func Publish(ctx context.Context, dapr *daprclient.Client, event Event) error {
	auditEvent := domain.AuditEvent{
		EventID:       event.EventID,
		EventType:     domain.TopicAuditEvent,
		CorrelationID: event.CorrelationID,
		TrackingID:    event.TrackingID,
		InvoiceID:     event.InvoiceID,
		Service:       event.Service,
		Action:        event.Action,
		Outcome:       event.Outcome,
		Reason:        event.Reason,
		Fields:        event.Fields,
		OccurredAtUTC: time.Now().UTC(),
	}

	return dapr.PublishEvent(ctx, domain.PubSubName, domain.TopicAuditEvent, auditEvent)
}
