package xai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	BillingCreditsURL  = DefaultCLIBaseURL + "/billing?format=credits"
	SettingsURL        = DefaultCLIBaseURL + "/settings"
	CLITokenAuthHeader = "xai-grok-cli"
)

type BillingSnapshot struct {
	CreditUsagePercent     float64 `json:"credit_usage_percent"`
	CreditRemainingPercent float64 `json:"credit_remaining_percent"`
	CurrentPeriodType      string  `json:"current_period_type"`
	CurrentPeriodStart     string  `json:"current_period_start"`
	CurrentPeriodEnd       string  `json:"current_period_end"`
	OnDemandCap            float64 `json:"on_demand_cap"`
	OnDemandUsed           float64 `json:"on_demand_used"`
	OnDemandRemaining      float64 `json:"on_demand_remaining"`
	PrepaidBalance         float64 `json:"prepaid_balance"`
	UnifiedBillingUser     bool    `json:"unified_billing_user"`
	TopUpMethod            string  `json:"top_up_method,omitempty"`
	BillingPeriodStart     string  `json:"billing_period_start,omitempty"`
	BillingPeriodEnd       string  `json:"billing_period_end,omitempty"`
	SubscriptionTier       string  `json:"subscription_tier,omitempty"`
	StatusCode             int     `json:"status_code,omitempty"`
	UpdatedAt              string  `json:"updated_at"`
}

type billingCreditsResponse struct {
	Config *billingCreditsConfig `json:"config"`
}

type billingCreditsConfig struct {
	CreditUsagePercent json.RawMessage       `json:"creditUsagePercent"`
	CurrentPeriod      *billingCreditsPeriod `json:"currentPeriod"`
	OnDemandCap        json.RawMessage       `json:"onDemandCap"`
	OnDemandUsed       json.RawMessage       `json:"onDemandUsed"`
	PrepaidBalance     json.RawMessage       `json:"prepaidBalance"`
	UnifiedBillingUser bool                  `json:"isUnifiedBillingUser"`
	TopUpMethod        string                `json:"topUpMethod"`
	BillingPeriodStart string                `json:"billingPeriodStart"`
	BillingPeriodEnd   string                `json:"billingPeriodEnd"`
}

type billingCreditsPeriod struct {
	Type  string `json:"type"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// ParseBillingCredits decodes the proto-JSON returned by the Grok CLI billing
// endpoint. Proto3 omits zero-valued fields, so absent numeric values are 0.
func ParseBillingCredits(body []byte, statusCode int) (*BillingSnapshot, error) {
	var payload billingCreditsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Grok billing response: %w", err)
	}
	if payload.Config == nil || payload.Config.CurrentPeriod == nil {
		return nil, fmt.Errorf("decode Grok billing response: missing config.currentPeriod")
	}

	periodType := strings.TrimSpace(payload.Config.CurrentPeriod.Type)
	periodStart, err := parseBillingTimestamp(payload.Config.CurrentPeriod.Start)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid current period start: %w", err)
	}
	periodEnd, err := parseBillingTimestamp(payload.Config.CurrentPeriod.End)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid current period end: %w", err)
	}
	if periodType == "" || !periodEnd.After(periodStart) {
		return nil, fmt.Errorf("decode Grok billing response: invalid current period")
	}

	usedPercent, err := parseOptionalBillingNumber(payload.Config.CreditUsagePercent)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid creditUsagePercent: %w", err)
	}
	onDemandCap, err := parseBillingAmount(payload.Config.OnDemandCap)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid onDemandCap: %w", err)
	}
	onDemandUsed, err := parseBillingAmount(payload.Config.OnDemandUsed)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid onDemandUsed: %w", err)
	}
	prepaidBalance, err := parseBillingAmount(payload.Config.PrepaidBalance)
	if err != nil {
		return nil, fmt.Errorf("decode Grok billing response: invalid prepaidBalance: %w", err)
	}
	usedPercent = math.Max(0, math.Min(100, usedPercent))
	onDemandCap = math.Max(0, onDemandCap)
	onDemandUsed = math.Max(0, onDemandUsed)
	prepaidBalance = math.Max(0, prepaidBalance)

	return &BillingSnapshot{
		CreditUsagePercent:     usedPercent,
		CreditRemainingPercent: math.Max(0, 100-usedPercent),
		CurrentPeriodType:      periodType,
		CurrentPeriodStart:     periodStart.UTC().Format(time.RFC3339Nano),
		CurrentPeriodEnd:       periodEnd.UTC().Format(time.RFC3339Nano),
		OnDemandCap:            onDemandCap,
		OnDemandUsed:           onDemandUsed,
		OnDemandRemaining:      math.Max(0, onDemandCap-onDemandUsed),
		PrepaidBalance:         prepaidBalance,
		UnifiedBillingUser:     payload.Config.UnifiedBillingUser,
		TopUpMethod:            strings.TrimSpace(payload.Config.TopUpMethod),
		BillingPeriodStart:     normalizeOptionalBillingTimestamp(payload.Config.BillingPeriodStart),
		BillingPeriodEnd:       normalizeOptionalBillingTimestamp(payload.Config.BillingPeriodEnd),
		StatusCode:             statusCode,
		UpdatedAt:              time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func ParseSettingsSubscriptionTier(body []byte) string {
	var payload struct {
		SubscriptionTierDisplay string `json:"subscription_tier_display"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.SubscriptionTierDisplay)
}

func parseBillingAmount(raw json.RawMessage) (float64, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	var amount struct {
		Val json.RawMessage `json:"val"`
	}
	if err := json.Unmarshal(raw, &amount); err != nil {
		return 0, err
	}
	return parseOptionalBillingNumber(amount.Val)
}

func parseOptionalBillingNumber(raw json.RawMessage) (float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}
	var number float64
	if err := json.Unmarshal(trimmed, &number); err == nil {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return 0, fmt.Errorf("number is not finite")
		}
		return number, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return 0, fmt.Errorf("expected number")
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("expected finite number")
	}
	return number, nil
}

func parseBillingTimestamp(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
}

func normalizeOptionalBillingTimestamp(raw string) string {
	parsed, err := parseBillingTimestamp(raw)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}
