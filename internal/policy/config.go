package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const DefaultPolicyConfigPath = "data/policy-config.json"

type Config struct {
	AutonomyCeilingUSD float64
	MinConfidence      float64
	FXRates            map[string]float64

	ReceiptRequiredAboveUSD       float64
	ForeignCurrencyHardStopUSD    float64
	UnknownCategoryMaxConfidence  float64
	MealMaxPerAttendeeUSD         float64
	MealClientReviewAboveUSD      float64
	TravelManagerReviewAboveUSD   float64
	SaaSMonthlyEligibleLimitUSD   float64
	HardwareCapitalReviewAboveUSD float64

	NewVendorRequiresReview       *bool
	MissingReceiptRequiresReview  *bool
	MathMismatchRequiresReview    *bool
	ForeignCurrencyRequiresReview *bool
}

type fileConfig struct {
	Autonomy struct {
		MaxAutoApproveUSD float64 `json:"max_auto_approve_usd"`
		MinConfidence     float64 `json:"min_confidence"`
	} `json:"autonomy"`

	ReviewTriggers struct {
		NewVendorRequiresReview       *bool `json:"new_vendor_requires_review"`
		MissingReceiptRequiresReview  *bool `json:"missing_receipt_requires_review"`
		MathMismatchRequiresReview    *bool `json:"math_mismatch_requires_review"`
		ForeignCurrencyRequiresReview *bool `json:"foreign_currency_requires_review"`
	} `json:"review_triggers"`

	Thresholds struct {
		ReceiptRequiredAboveUSD       float64 `json:"receipt_required_above_usd"`
		ForeignCurrencyHardStopUSD    float64 `json:"foreign_currency_hard_stop_usd"`
		UnknownCategoryMaxConfidence  float64 `json:"unknown_category_max_confidence"`
		MealsMaxPerAttendeeUSD        float64 `json:"meals_max_per_attendee_usd"`
		MealsClientReviewAboveUSD     float64 `json:"meals_client_entertainment_review_above_usd"`
		TravelManagerReviewAboveUSD   float64 `json:"travel_manager_review_above_usd"`
		SaaSMonthlyEligibleLimitUSD   float64 `json:"saas_monthly_auto_eligible_limit_usd"`
		HardwareCapitalReviewAboveUSD float64 `json:"hardware_capital_expense_review_above_usd"`
	} `json:"thresholds"`

	FXRates map[string]float64 `json:"fx_rates"`
}

func DefaultConfig() Config {
	return Config{
		AutonomyCeilingUSD:            250,
		MinConfidence:                 0.80,
		FXRates:                       map[string]float64{"USD": 1.0, "EUR": 1.08, "GBP": 1.27},
		ReceiptRequiredAboveUSD:       25,
		ForeignCurrencyHardStopUSD:    1000,
		UnknownCategoryMaxConfidence:  0.50,
		MealMaxPerAttendeeUSD:         75,
		MealClientReviewAboveUSD:      500,
		TravelManagerReviewAboveUSD:   1500,
		SaaSMonthlyEligibleLimitUSD:   200,
		HardwareCapitalReviewAboveUSD: 1000,
		NewVendorRequiresReview:       boolPtr(true),
		MissingReceiptRequiresReview:  boolPtr(true),
		MathMismatchRequiresReview:    boolPtr(true),
		ForeignCurrencyRequiresReview: boolPtr(true),
	}
}

func LoadConfigFromEnv() (Config, error) {
	path := strings.TrimSpace(os.Getenv("POLICY_CONFIG_PATH"))
	if path == "" {
		path = DefaultPolicyConfigPath
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		return Config{}, err
	}

	return applyLegacyEnvOverrides(cfg), nil
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	path := strings.TrimSpace(os.Getenv("POLICY_CONFIG_PATH"))
	if path != "" {
		if loaded, err := LoadConfig(path); err == nil {
			cfg = loaded
		}
	}

	return applyLegacyEnvOverrides(cfg)
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPolicyConfigPath
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read policy config %q: %w", path, err)
	}

	var raw fileConfig
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return Config{}, fmt.Errorf("decode policy config %q: %w", path, err)
	}

	cfg := DefaultConfig()

	if raw.Autonomy.MaxAutoApproveUSD != 0 {
		cfg.AutonomyCeilingUSD = raw.Autonomy.MaxAutoApproveUSD
	}
	if raw.Autonomy.MinConfidence != 0 {
		cfg.MinConfidence = raw.Autonomy.MinConfidence
	}

	if raw.Thresholds.ReceiptRequiredAboveUSD != 0 {
		cfg.ReceiptRequiredAboveUSD = raw.Thresholds.ReceiptRequiredAboveUSD
	}
	if raw.Thresholds.ForeignCurrencyHardStopUSD != 0 {
		cfg.ForeignCurrencyHardStopUSD = raw.Thresholds.ForeignCurrencyHardStopUSD
	}
	if raw.Thresholds.UnknownCategoryMaxConfidence != 0 {
		cfg.UnknownCategoryMaxConfidence = raw.Thresholds.UnknownCategoryMaxConfidence
	}
	if raw.Thresholds.MealsMaxPerAttendeeUSD != 0 {
		cfg.MealMaxPerAttendeeUSD = raw.Thresholds.MealsMaxPerAttendeeUSD
	}
	if raw.Thresholds.MealsClientReviewAboveUSD != 0 {
		cfg.MealClientReviewAboveUSD = raw.Thresholds.MealsClientReviewAboveUSD
	}
	if raw.Thresholds.TravelManagerReviewAboveUSD != 0 {
		cfg.TravelManagerReviewAboveUSD = raw.Thresholds.TravelManagerReviewAboveUSD
	}
	if raw.Thresholds.SaaSMonthlyEligibleLimitUSD != 0 {
		cfg.SaaSMonthlyEligibleLimitUSD = raw.Thresholds.SaaSMonthlyEligibleLimitUSD
	}
	if raw.Thresholds.HardwareCapitalReviewAboveUSD != 0 {
		cfg.HardwareCapitalReviewAboveUSD = raw.Thresholds.HardwareCapitalReviewAboveUSD
	}

	if raw.FXRates != nil {
		cfg.FXRates = normalizeRates(raw.FXRates)
	}

	if raw.ReviewTriggers.NewVendorRequiresReview != nil {
		cfg.NewVendorRequiresReview = raw.ReviewTriggers.NewVendorRequiresReview
	}
	if raw.ReviewTriggers.MissingReceiptRequiresReview != nil {
		cfg.MissingReceiptRequiresReview = raw.ReviewTriggers.MissingReceiptRequiresReview
	}
	if raw.ReviewTriggers.MathMismatchRequiresReview != nil {
		cfg.MathMismatchRequiresReview = raw.ReviewTriggers.MathMismatchRequiresReview
	}
	if raw.ReviewTriggers.ForeignCurrencyRequiresReview != nil {
		cfg.ForeignCurrencyRequiresReview = raw.ReviewTriggers.ForeignCurrencyRequiresReview
	}

	cfg = normalizeConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("invalid policy config %q: %w", path, err)
	}

	return cfg, nil
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()

	if cfg.AutonomyCeilingUSD == 0 {
		cfg.AutonomyCeilingUSD = def.AutonomyCeilingUSD
	}
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = def.MinConfidence
	}
	if cfg.FXRates == nil {
		cfg.FXRates = def.FXRates
	}
	if cfg.ReceiptRequiredAboveUSD == 0 {
		cfg.ReceiptRequiredAboveUSD = def.ReceiptRequiredAboveUSD
	}
	if cfg.ForeignCurrencyHardStopUSD == 0 {
		cfg.ForeignCurrencyHardStopUSD = def.ForeignCurrencyHardStopUSD
	}
	if cfg.UnknownCategoryMaxConfidence == 0 {
		cfg.UnknownCategoryMaxConfidence = def.UnknownCategoryMaxConfidence
	}
	if cfg.MealMaxPerAttendeeUSD == 0 {
		cfg.MealMaxPerAttendeeUSD = def.MealMaxPerAttendeeUSD
	}
	if cfg.MealClientReviewAboveUSD == 0 {
		cfg.MealClientReviewAboveUSD = def.MealClientReviewAboveUSD
	}
	if cfg.TravelManagerReviewAboveUSD == 0 {
		cfg.TravelManagerReviewAboveUSD = def.TravelManagerReviewAboveUSD
	}
	if cfg.SaaSMonthlyEligibleLimitUSD == 0 {
		cfg.SaaSMonthlyEligibleLimitUSD = def.SaaSMonthlyEligibleLimitUSD
	}
	if cfg.HardwareCapitalReviewAboveUSD == 0 {
		cfg.HardwareCapitalReviewAboveUSD = def.HardwareCapitalReviewAboveUSD
	}

	return cfg
}

func validateConfig(cfg Config) error {
	if cfg.AutonomyCeilingUSD <= 0 {
		return fmt.Errorf("autonomy ceiling must be positive")
	}
	if cfg.MinConfidence <= 0 || cfg.MinConfidence > 1 {
		return fmt.Errorf("min confidence must be within (0, 1]")
	}
	if cfg.ReceiptRequiredAboveUSD < 0 {
		return fmt.Errorf("receipt threshold cannot be negative")
	}
	if cfg.ForeignCurrencyHardStopUSD <= 0 {
		return fmt.Errorf("foreign currency hard-stop must be positive")
	}
	if cfg.UnknownCategoryMaxConfidence <= 0 || cfg.UnknownCategoryMaxConfidence > 1 {
		return fmt.Errorf("unknown category confidence must be within (0, 1]")
	}
	if cfg.FXRates["USD"] != 1.0 {
		return fmt.Errorf("USD FX rate must be 1.0")
	}

	return nil
}

func applyLegacyEnvOverrides(cfg Config) Config {
	if value, ok := getFloatEnv("AUTONOMY_CEILING_USD"); ok {
		cfg.AutonomyCeilingUSD = value
	}
	if value, ok := getFloatEnv("AUTONOMY_CONFIDENCE"); ok {
		cfg.MinConfidence = value
	}

	return normalizeConfig(cfg)
}

func getFloatEnv(key string) (float64, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}

	return value, true
}

func normalizeRates(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for currency, rate := range in {
		out[strings.ToUpper(strings.TrimSpace(currency))] = rate
	}
	return out
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func boolPtr(value bool) *bool {
	return &value
}
