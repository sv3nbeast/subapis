package service

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	grokQuotaSnapshotExtraKey   = "grok_usage_snapshot"
	grokBillingSnapshotExtraKey = "grok_billing_snapshot"
)

type GrokQuotaFetcher struct{}

func NewGrokQuotaFetcher() *GrokQuotaFetcher {
	return &GrokQuotaFetcher{}
}

func (f *GrokQuotaFetcher) BuildUsageInfo(account *Account) *UsageInfo {
	now := time.Now()
	usage := &UsageInfo{
		Source:    "passive",
		UpdatedAt: &now,
	}
	if account == nil {
		usage.ErrorCode = "billing_unknown"
		usage.Error = "Grok billing usage has not been queried"
		usage.GrokBillingState = "unknown"
		return usage
	}

	billing, billingErr := grokBillingSnapshotFromExtra(account.Extra)
	if billingErr == nil && billing != nil {
		usage.GrokBilling = billing
		usage.GrokBillingState = "observed"
		usage.SubscriptionTier = billing.SubscriptionTier
		usage.SubscriptionTierRaw = billing.SubscriptionTier
		if parsedAt, err := time.Parse(time.RFC3339, billing.UpdatedAt); err == nil {
			usage.UpdatedAt = &parsedAt
		}
	} else {
		usage.GrokBillingState = "unknown"
		usage.ErrorCode = "billing_unknown"
		usage.Error = "Grok billing usage has not been queried"
	}

	snapshot, snapshotErr := grokQuotaSnapshotFromExtra(account.Extra)
	if snapshotErr != nil || snapshot == nil {
		usage.GrokQuotaSnapshotState = "unknown_until_first_response"
		return usage
	}

	if usage.GrokBilling == nil {
		if parsedAt, err := time.Parse(time.RFC3339, snapshot.UpdatedAt); err == nil {
			usage.UpdatedAt = &parsedAt
		}
	}
	usage.GrokRequestQuota = snapshot.Requests
	usage.GrokTokenQuota = snapshot.Tokens
	usage.GrokRetryAfterSeconds = snapshot.RetryAfterSeconds
	if usage.SubscriptionTier == "" {
		usage.SubscriptionTier = snapshot.SubscriptionTier
		usage.SubscriptionTierRaw = snapshot.SubscriptionTier
	}
	usage.GrokEntitlementStatus = snapshot.EntitlementStatus
	usage.GrokLastQuotaProbeAt = snapshot.LastProbeAt
	usage.GrokLastHeadersSeenAt = snapshot.LastHeadersSeenAt
	usage.GrokLastStatusCode = snapshot.StatusCode
	if snapshot.HasObservedHeaders() {
		usage.GrokQuotaSnapshotState = "observed"
	} else {
		usage.GrokQuotaSnapshotState = "no_headers"
	}

	switch snapshot.StatusCode {
	case 401:
		usage.NeedsReauth = true
		usage.ErrorCode = "unauthenticated"
	case 403:
		usage.IsForbidden = true
		usage.ForbiddenType = "forbidden"
		usage.ErrorCode = "forbidden"
		if usage.GrokEntitlementStatus == "" {
			usage.GrokEntitlementStatus = "forbidden"
		}
	case 429:
		usage.ErrorCode = "rate_limited"
	}
	return usage
}

func grokQuotaSnapshotFromExtra(extra map[string]any) (*xai.QuotaSnapshot, error) {
	if extra == nil {
		return nil, nil
	}
	raw, ok := extra[grokQuotaSnapshotExtraKey]
	if !ok || raw == nil {
		return nil, nil
	}
	switch snapshot := raw.(type) {
	case *xai.QuotaSnapshot:
		return snapshot, nil
	case xai.QuotaSnapshot:
		return &snapshot, nil
	case map[string]any:
		data, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		var out xai.QuotaSnapshot
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal grok quota snapshot: %w", err)
		}
		var out xai.QuotaSnapshot
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
}

func grokBillingSnapshotFromExtra(extra map[string]any) (*xai.BillingSnapshot, error) {
	if extra == nil {
		return nil, nil
	}
	raw, ok := extra[grokBillingSnapshotExtraKey]
	if !ok || raw == nil {
		return nil, nil
	}
	switch snapshot := raw.(type) {
	case *xai.BillingSnapshot:
		return snapshot, nil
	case xai.BillingSnapshot:
		return &snapshot, nil
	case map[string]any:
		data, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		var out xai.BillingSnapshot
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal grok billing snapshot: %w", err)
		}
		var out xai.BillingSnapshot
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
}
