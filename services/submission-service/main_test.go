package main

import (
	"testing"

	"approvalflow/internal/domain"
)

func validSubmissionRequestForTest() domain.SubmissionRequest {
	return domain.SubmissionRequest{
		ID:             "INV-TEST-001",
		Submitter:      "alice@example.com",
		Department:     "engineering",
		Vendor:         "Known Vendor",
		VendorKnown:    true,
		InvoiceNumber:  "INV-001",
		Currency:       "USD",
		Category:       "saas",
		Total:          99,
		ReceiptPresent: true,
		Date:           "2026-05-30",
	}
}

func TestValidateSubmissionAcceptsValidRequest(t *testing.T) {
	req := validSubmissionRequestForTest()

	if err := validateSubmission(req); err != nil {
		t.Fatalf("expected valid submission, got error: %v", err)
	}
}

func TestValidateSubmissionRejectsInvalidRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SubmissionRequest)
	}{
		{
			name: "missing vendor",
			mutate: func(req *domain.SubmissionRequest) {
				req.Vendor = ""
			},
		},
		{
			name: "missing invoice number",
			mutate: func(req *domain.SubmissionRequest) {
				req.InvoiceNumber = ""
			},
		},
		{
			name: "zero total",
			mutate: func(req *domain.SubmissionRequest) {
				req.Total = 0
			},
		},
		{
			name: "negative total",
			mutate: func(req *domain.SubmissionRequest) {
				req.Total = -10
			},
		},
		{
			name: "missing currency",
			mutate: func(req *domain.SubmissionRequest) {
				req.Currency = ""
			},
		},
		{
			name: "missing category",
			mutate: func(req *domain.SubmissionRequest) {
				req.Category = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validSubmissionRequestForTest()
			tt.mutate(&req)

			if err := validateSubmission(req); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestDuplicateFingerprintIsStableAndNormalized(t *testing.T) {
	left := validSubmissionRequestForTest()
	left.Vendor = "  Known Vendor  "
	left.InvoiceNumber = " Inv-001 "
	left.Total = 99

	right := validSubmissionRequestForTest()
	right.Vendor = "known vendor"
	right.InvoiceNumber = "inv-001"
	right.Total = 99

	if duplicateFingerprint(left) != duplicateFingerprint(right) {
		t.Fatalf("expected duplicate fingerprint to normalize vendor and invoice number")
	}
}

func TestDuplicateFingerprintChangesWhenBusinessIdentityChanges(t *testing.T) {
	base := validSubmissionRequestForTest()

	changedVendor := base
	changedVendor.Vendor = "Other Vendor"

	changedInvoice := base
	changedInvoice.InvoiceNumber = "INV-002"

	changedTotal := base
	changedTotal.Total = 100

	baseFingerprint := duplicateFingerprint(base)

	if duplicateFingerprint(changedVendor) == baseFingerprint {
		t.Fatalf("expected vendor change to change duplicate fingerprint")
	}

	if duplicateFingerprint(changedInvoice) == baseFingerprint {
		t.Fatalf("expected invoice number change to change duplicate fingerprint")
	}

	if duplicateFingerprint(changedTotal) == baseFingerprint {
		t.Fatalf("expected total change to change duplicate fingerprint")
	}
}

func TestApplyAdditionalInfoToRecord(t *testing.T) {
	notes := "Client: Contoso. Business justification added."
	receiptPresent := true
	attendees := 11

	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(&record, domain.AdditionalInfoRequest{
		Notes:          &notes,
		ReceiptPresent: &receiptPresent,
		Attendees:      &attendees,
	})
	if err != nil {
		t.Fatalf("expected additional info to be accepted: %v", err)
	}

	if record.Request.Notes != notes {
		t.Fatalf("expected notes to be updated")
	}

	if !record.Request.ReceiptPresent {
		t.Fatalf("expected receiptPresent to be updated")
	}

	if record.Request.Attendees == nil || *record.Request.Attendees != attendees {
		t.Fatalf("expected attendees to be updated")
	}
}

func TestApplyAdditionalInfoToRecordRequiresAtLeastOneField(t *testing.T) {
	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(
		&record,
		domain.AdditionalInfoRequest{},
	)
	if err == nil {
		t.Fatalf("expected empty additional info payload to be rejected")
	}
}

func TestApplyAdditionalInfoToRecordRejectsInvalidAttendees(t *testing.T) {
	attendees := 0
	record := domain.SubmissionRecord{
		Request: validSubmissionRequestForTest(),
	}

	err := applyAdditionalInfoToRecord(
		&record,
		domain.AdditionalInfoRequest{
			Attendees: &attendees,
		},
	)
	if err == nil {
		t.Fatalf("expected non-positive attendees to be rejected")
	}
}
