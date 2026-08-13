package xai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	BillingCreditsURL  = DefaultCLIBaseURL + "/billing?format=credits"
	SettingsURL        = DefaultCLIBaseURL + "/settings"
	CLITokenAuthHeader = "xai-grok-cli"
)

const (
	BillingWeeklyPath  = "/billing?format=credits"
	BillingMonthlyPath = "/billing"

	SuperGrokLimitCents      = 15_000
	SuperGrokHeavyLimitCents = 150_000
)

// BillingPeriod describes the current weekly/monthly window.
type BillingPeriod struct {
	Type  string `json:"type,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// BillingProductUsage is per-product usage inside the weekly credits window.
type BillingProductUsage struct {
	Product      string   `json:"product,omitempty"`
	UsagePercent *float64 `json:"usagePercent,omitempty"`
}

// BillingConfig is the nested config object from /v1/billing responses.
// Weekly (`?format=credits`) and monthly (`/billing`) share this shape; absolute
// money fields typically appear on the credits (prepaid/on-demand) or monthly
// (limit/used) responses.
type BillingConfig struct {
	CurrentPeriod        *BillingPeriod        `json:"currentPeriod,omitempty"`
	CreditUsagePercent   *float64              `json:"creditUsagePercent,omitempty"`
	ProductUsage         []BillingProductUsage `json:"productUsage,omitempty"`
	MonthlyLimit         json.RawMessage       `json:"monthlyLimit,omitempty"`
	Used                 json.RawMessage       `json:"used,omitempty"`
	OnDemandCap          json.RawMessage       `json:"onDemandCap,omitempty"`
	OnDemandUsed         json.RawMessage       `json:"onDemandUsed,omitempty"`
	PrepaidBalance       json.RawMessage       `json:"prepaidBalance,omitempty"`
	IsUnifiedBillingUser bool                  `json:"isUnifiedBillingUser,omitempty"`
	TopUpMethod          string                `json:"topUpMethod,omitempty"`
	BillingPeriodStart   string                `json:"billingPeriodStart,omitempty"`
	BillingPeriodEnd     string                `json:"billingPeriodEnd,omitempty"`
}

// BillingPayload is the top-level body from /v1/billing.
type BillingPayload struct {
	Config *BillingConfig `json:"config,omitempty"`
}

type BillingProductSummary struct {
	Product      string   `json:"product"`
	UsagePercent *float64 `json:"usage_percent,omitempty"`
}

type BillingSummary struct {
	PeriodType         string                  `json:"period_type,omitempty"`
	UsagePercent       *float64                `json:"usage_percent,omitempty"`
	PeriodStart        string                  `json:"period_start,omitempty"`
	PeriodEnd          string                  `json:"period_end,omitempty"`
	ProductUsage       []BillingProductSummary `json:"product_usage,omitempty"`
	MonthlyLimitCents  *float64                `json:"monthly_limit_cents,omitempty"`
	UsedCents          *float64                `json:"used_cents,omitempty"`
	IncludedUsedCents  *float64                `json:"included_used_cents,omitempty"`
	BillingPeriodStart string                  `json:"billing_period_start,omitempty"`
	BillingPeriodEnd   string                  `json:"billing_period_end,omitempty"`
	UsedPercent        *float64                `json:"used_percent,omitempty"`
	Plan               string                  `json:"plan,omitempty"`
	StatusCode         int                     `json:"status_code,omitempty"`
	WeeklyStatusCode   int                     `json:"weekly_status_code,omitempty"`
	MonthlyStatusCode  int                     `json:"monthly_status_code,omitempty"`
	Source             string                  `json:"source,omitempty"`
	FetchedAt          string                  `json:"fetched_at,omitempty"`
	UpdatedAt          string                  `json:"updated_at,omitempty"`
	WeeklyUpdatedAt    string                  `json:"weekly_updated_at,omitempty"`
	MonthlyUpdatedAt   string                  `json:"monthly_updated_at,omitempty"`
	Partial            bool                    `json:"partial,omitempty"`
	FailedWindows      []string                `json:"failed_windows,omitempty"`

	// Legacy fields remain readable so existing production snapshots and
	// cooldown decisions survive the transition to the merged billing view.
	CreditUsagePercent     float64 `json:"credit_usage_percent,omitempty"`
	CreditRemainingPercent float64 `json:"credit_remaining_percent,omitempty"`
	CurrentPeriodType      string  `json:"current_period_type,omitempty"`
	CurrentPeriodStart     string  `json:"current_period_start,omitempty"`
	CurrentPeriodEnd       string  `json:"current_period_end,omitempty"`
	OnDemandCap            float64 `json:"on_demand_cap,omitempty"`
	OnDemandUsed           float64 `json:"on_demand_used,omitempty"`
	OnDemandRemaining      float64 `json:"on_demand_remaining,omitempty"`
	PrepaidBalance         float64 `json:"prepaid_balance,omitempty"`
	UnifiedBillingUser     bool    `json:"unified_billing_user,omitempty"`
	TopUpMethod            string  `json:"top_up_method,omitempty"`
	SubscriptionTier       string  `json:"subscription_tier,omitempty"`
}

// BillingSnapshot is retained as a source-compatible name for the legacy
// production quota path. Both names serialize to the same forward-compatible
// shape.
type BillingSnapshot = BillingSummary

// BuildBillingURL builds weekly or monthly billing URL against the CLI chat proxy.
func BuildBillingURL(formatCredits bool) string {
	base := strings.TrimRight(DefaultCLIBaseURL, "/")
	if formatCredits {
		return base + BillingWeeklyPath
	}
	return base + BillingMonthlyPath
}

func BuildBillingURLWithValidator(baseURL string, formatCredits bool, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	if formatCredits {
		return validatedBaseURL + BillingWeeklyPath, nil
	}
	return validatedBaseURL + BillingMonthlyPath, nil
}

// ApplyCLIBillingHeaders sets Authorization and the Grok CLI identity used by
// the billing endpoints. These constants are shared with cli_headers.go.
func ApplyCLIBillingHeaders(req *http.Request, accessToken string) {
	if req == nil {
		return
	}
	if token := strings.TrimSpace(accessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CLITokenAuthHdr, CLITokenAuthHeader)
	req.Header.Set(CLIClientVersionHdr, CLIClientVersion)
	req.Header.Set("User-Agent", CLIUserAgentDefault)
}

// ParseBillingPayload unmarshals a billing API response body.
func ParseBillingPayload(body []byte) (*BillingPayload, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty billing body")
	}
	var payload BillingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
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

// BuildBillingSummary normalizes a billing config into a UI-friendly summary.
func BuildBillingSummary(config *BillingConfig) *BillingSummary {
	if config == nil {
		return nil
	}
	periodType := resolvePeriodType(config.CurrentPeriod)
	periodStart, periodEnd := "", ""
	if config.CurrentPeriod != nil {
		periodStart = strings.TrimSpace(config.CurrentPeriod.Start)
		periodEnd = strings.TrimSpace(config.CurrentPeriod.End)
	}

	products := make([]BillingProductSummary, 0, len(config.ProductUsage))
	for _, item := range config.ProductUsage {
		if product := strings.TrimSpace(item.Product); product != "" {
			products = append(products, BillingProductSummary{Product: product, UsagePercent: cloneFloat(item.UsagePercent)})
		}
	}

	monthlyLimit := parseCentValue(config.MonthlyLimit)
	used := parseCentValue(config.Used)
	var includedUsed, usedPercent *float64
	if used != nil {
		included := *used
		if monthlyLimit != nil && *monthlyLimit > 0 {
			included = math.Min(included, *monthlyLimit)
		}
		includedUsed = &included
		if monthlyLimit != nil && *monthlyLimit > 0 {
			percent := included / *monthlyLimit * 100
			usedPercent = &percent
		}
	}

	weeklyUsage := cloneFloat(config.CreditUsagePercent)
	hasWeekly := weeklyUsage != nil || periodType == "weekly" || len(products) > 0
	hasMonthly := monthlyLimit != nil || used != nil || (!hasWeekly && strings.TrimSpace(config.BillingPeriodEnd) != "")
	if !hasWeekly && !hasMonthly {
		return nil
	}

	summary := &BillingSummary{
		UsagePercent:       weeklyUsage,
		ProductUsage:       products,
		MonthlyLimitCents:  monthlyLimit,
		UsedCents:          used,
		IncludedUsedCents:  includedUsed,
		UsedPercent:        usedPercent,
		Plan:               resolvePlan(monthlyLimit),
		BillingPeriodStart: strings.TrimSpace(config.BillingPeriodStart),
		BillingPeriodEnd:   strings.TrimSpace(config.BillingPeriodEnd),
	}
	if hasWeekly {
		if periodType == "unknown" {
			periodType = "weekly"
		}
		summary.PeriodType = periodType
		summary.PeriodStart = periodStart
		summary.PeriodEnd = periodEnd
	} else {
		summary.PeriodType = "monthly"
		summary.PeriodStart = summary.BillingPeriodStart
		summary.PeriodEnd = summary.BillingPeriodEnd
	}
	return summary
}

// MergeBillingProbeResult updates successful billing domains while retaining
// the previous value for any domain that could not be refreshed.
func MergeBillingProbeResult(previous, weekly, monthly *BillingSummary, weeklyOK, monthlyOK bool) *BillingSummary {
	var out BillingSummary
	if previous != nil {
		out = *previous
		previousUpdatedAt := firstNonEmptyBilling(previous.UpdatedAt, previous.FetchedAt)
		if out.WeeklyUpdatedAt == "" && (out.UsagePercent != nil || len(out.ProductUsage) > 0) {
			out.WeeklyUpdatedAt = previousUpdatedAt
		}
		if out.MonthlyUpdatedAt == "" && (out.MonthlyLimitCents != nil || out.UsedPercent != nil) {
			out.MonthlyUpdatedAt = previousUpdatedAt
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if weeklyOK && weekly != nil {
		out.PeriodType = weekly.PeriodType
		out.UsagePercent = cloneFloat(weekly.UsagePercent)
		out.PeriodStart = weekly.PeriodStart
		out.PeriodEnd = weekly.PeriodEnd
		out.ProductUsage = append([]BillingProductSummary(nil), weekly.ProductUsage...)
		out.WeeklyUpdatedAt = now
	}
	if monthlyOK && monthly != nil {
		if out.PeriodType == "" {
			out.PeriodType = "monthly"
		}
		out.MonthlyLimitCents = cloneFloat(monthly.MonthlyLimitCents)
		out.UsedCents = cloneFloat(monthly.UsedCents)
		out.IncludedUsedCents = cloneFloat(monthly.IncludedUsedCents)
		out.BillingPeriodStart = monthly.BillingPeriodStart
		out.BillingPeriodEnd = monthly.BillingPeriodEnd
		out.UsedPercent = cloneFloat(monthly.UsedPercent)
		out.Plan = monthly.Plan
		out.MonthlyUpdatedAt = now
	}
	out.Partial = !weeklyOK || !monthlyOK
	out.FailedWindows = nil
	if !weeklyOK {
		out.FailedWindows = append(out.FailedWindows, "weekly")
	}
	if !monthlyOK {
		out.FailedWindows = append(out.FailedWindows, "monthly")
	}
	if !weeklyOK && !monthlyOK && previous == nil {
		return nil
	}
	return &out
}

// StampBillingSummary sets fetch metadata.
func StampBillingSummary(summary *BillingSummary, statusCode int, source string) *BillingSummary {
	if summary == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	summary.StatusCode = statusCode
	summary.Source = source
	summary.FetchedAt = now
	summary.UpdatedAt = now
	return summary
}

func resolvePeriodType(period *BillingPeriod) string {
	if period == nil {
		return "unknown"
	}
	raw := strings.ToLower(strings.TrimSpace(period.Type))
	if strings.Contains(raw, "weekly") {
		return "weekly"
	}
	if strings.Contains(raw, "monthly") {
		return "monthly"
	}
	return "unknown"
}

func resolvePlan(monthlyLimitCents *float64) string {
	if monthlyLimitCents == nil {
		return ""
	}
	switch math.Round(*monthlyLimitCents) {
	case SuperGrokLimitCents:
		return "SuperGrok"
	case SuperGrokHeavyLimitCents:
		return "SuperGrok Heavy"
	default:
		return ""
	}
}

func parseCentValue(raw json.RawMessage) *float64 {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var object struct {
		Val any `json:"val"`
	}
	if err := json.Unmarshal(trimmed, &object); err == nil && object.Val != nil {
		return anyToFloat(object.Val)
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil
	}
	return anyToFloat(value)
}

func anyToFloat(value any) *float64 {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return nil
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		number = parsed
	default:
		return nil
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func firstNonEmptyBilling(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
