package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigReadsPolicyConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy-config.json")

	raw := []byte(`{
	  "autonomy": {
	    "max_auto_approve_usd": 123,
	    "min_confidence": 0.91
	  },
	  "thresholds": {
	    "receipt_required_above_usd": 10,
	    "foreign_currency_hard_stop_usd": 900,
	    "unknown_category_max_confidence": 0.4,
	    "meals_max_per_attendee_usd": 55,
	    "meals_client_entertainment_review_above_usd": 400,
	    "travel_manager_review_above_usd": 1200,
	    "saas_monthly_auto_eligible_limit_usd": 150,
	    "hardware_capital_expense_review_above_usd": 800
	  },
	  "fx_rates": {
	    "USD": 1.0,
	    "EUR": 1.1
	  }
	}`)

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.AutonomyCeilingUSD != 123 {
		t.Fatalf("expected ceiling 123, got %.2f", cfg.AutonomyCeilingUSD)
	}

	if cfg.MinConfidence != 0.91 {
		t.Fatalf("expected min confidence 0.91, got %.2f", cfg.MinConfidence)
	}

	if cfg.SaaSMonthlyEligibleLimitUSD != 150 {
		t.Fatalf("expected saas limit 150, got %.2f", cfg.SaaSMonthlyEligibleLimitUSD)
	}

	if cfg.FXRates["EUR"] != 1.1 {
		t.Fatalf("expected EUR FX rate 1.1, got %.2f", cfg.FXRates["EUR"])
	}
}

func TestLoadConfigRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy-config.json")

	raw := []byte(`{
	  "autonomy": {
	    "max_auto_approve_usd": -1,
	    "min_confidence": 0.8
	  },
	  "fx_rates": {
	    "USD": 1.0
	  }
	}`)

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatalf("expected invalid config error")
	}
}

func TestConfigFromEnvKeepsLegacyOverrides(t *testing.T) {
	t.Setenv("AUTONOMY_CEILING_USD", "10")
	t.Setenv("AUTONOMY_CONFIDENCE", "0.99")

	cfg := ConfigFromEnv()

	if cfg.AutonomyCeilingUSD != 10 {
		t.Fatalf("expected autonomy ceiling 10, got %.2f", cfg.AutonomyCeilingUSD)
	}

	if cfg.MinConfidence != 0.99 {
		t.Fatalf("expected min confidence 0.99, got %.2f", cfg.MinConfidence)
	}
}
