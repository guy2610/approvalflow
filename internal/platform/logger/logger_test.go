package logger

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

func TestLoggerWritesStructuredJSON(t *testing.T) {
	var buf bytes.Buffer

	l := New("test-service")
	l.base = log.New(&buf, "", 0)

	l.Info("hello", Fields{"tracking_id": "sub_123"})

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("expected log output")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("expected valid json log entry: %v", err)
	}

	if entry["level"] != "info" {
		t.Fatalf("expected level info, got %v", entry["level"])
	}

	if entry["service"] != "test-service" {
		t.Fatalf("expected service test-service, got %v", entry["service"])
	}

	if entry["message"] != "hello" {
		t.Fatalf("expected message hello, got %v", entry["message"])
	}

	if entry["tracking_id"] != "sub_123" {
		t.Fatalf("expected tracking_id sub_123, got %v", entry["tracking_id"])
	}

	if entry["time_utc"] == "" {
		t.Fatalf("expected time_utc to be populated")
	}
}

func TestLoggerHandlesNilFields(t *testing.T) {
	var buf bytes.Buffer

	l := New("test-service")
	l.base = log.New(&buf, "", 0)

	l.Error("boom", nil)

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("expected valid json log entry: %v", err)
	}

	if entry["level"] != "error" {
		t.Fatalf("expected level error, got %v", entry["level"])
	}

	if entry["message"] != "boom" {
		t.Fatalf("expected message boom, got %v", entry["message"])
	}
}
