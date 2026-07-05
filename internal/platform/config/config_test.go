package config

import "testing"

func TestGetEnvReturnsFallback(t *testing.T) {
	t.Setenv("APPROVALFLOW_TEST_EMPTY", "")

	got := GetEnv("APPROVALFLOW_TEST_EMPTY", "fallback")
	if got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestGetEnvReturnsValue(t *testing.T) {
	t.Setenv("APPROVALFLOW_TEST_VALUE", "actual")

	got := GetEnv("APPROVALFLOW_TEST_VALUE", "fallback")
	if got != "actual" {
		t.Fatalf("expected actual, got %q", got)
	}
}
