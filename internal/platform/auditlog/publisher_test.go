package auditlog

import "testing"

func TestEventCarriesAuditFields(t *testing.T) {
	event := Event{
		EventID:       "audit-1",
		CorrelationID: "corr-1",
		TrackingID:    "sub-1",
		InvoiceID:     "INV-1",
		Service:       "test-service",
		Action:        "decision_produced",
		Outcome:       "auto_approve",
		Reason:        "test reason",
		Fields: map[string]any{
			"amount_usd": 42.0,
		},
	}

	if event.EventID == "" || event.CorrelationID == "" {
		t.Fatalf("expected event identity fields to be populated")
	}

	if event.Fields["amount_usd"] != 42.0 {
		t.Fatalf("expected amount_usd field to be preserved")
	}
}
