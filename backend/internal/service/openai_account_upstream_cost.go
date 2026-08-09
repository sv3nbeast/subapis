package service

import (
	"math"
	"sort"
	"time"
)

func openAIUpstreamCostFactors(accounts []*Account, now time.Time, oauthSchedulingRateMultiplier float64) map[int64]float64 {
	type rateSample struct {
		accountID int64
		rate      float64
	}

	factors := make(map[int64]float64, len(accounts))
	samples := make([]rateSample, 0, len(accounts))
	eligibleCount := 0
	for _, account := range accounts {
		if account == nil {
			continue
		}
		factors[account.ID] = openAIUpstreamCostNeutralFactor
		if !account.IsOpenAIApiKey() && !account.IsOpenAIOAuth() {
			continue
		}
		eligibleCount++
		if rate, ok := openAISchedulingRate(account, now, oauthSchedulingRateMultiplier); ok {
			samples = append(samples, rateSample{accountID: account.ID, rate: rate})
		}
	}
	if len(samples) < 2 || eligibleCount == 0 {
		return factors
	}

	allEqual := true
	positiveLogs := make([]float64, 0, len(samples))
	for i, sample := range samples {
		if i > 0 && sample.rate != samples[0].rate {
			allEqual = false
		}
		if sample.rate > 0 {
			positiveLogs = append(positiveLogs, math.Log(sample.rate))
		}
	}
	if allEqual || len(positiveLogs) == 0 {
		return factors
	}

	sort.Float64s(positiveLogs)
	middle := len(positiveLogs) / 2
	medianLog := positiveLogs[middle]
	if len(positiveLogs)%2 == 0 {
		medianLog = (positiveLogs[middle-1] + positiveLogs[middle]) / 2
	}
	center := math.Exp(medianLog)
	if center <= 0 || math.IsNaN(center) || math.IsInf(center, 0) {
		return factors
	}

	coverage := float64(len(samples)) / float64(eligibleCount)
	for _, sample := range samples {
		rawFactor := 1.0
		if sample.rate > 0 {
			rawFactor = 1 / (1 + sample.rate/center)
		}
		factors[sample.accountID] = clamp01(openAIUpstreamCostNeutralFactor + coverage*(rawFactor-openAIUpstreamCostNeutralFactor))
	}
	return factors
}

type openAILegacyUpstreamRateOrder struct {
	enabled bool
	rates   map[int64]float64
}

func newOpenAILegacyUpstreamRateOrder(accounts []*Account, now time.Time, oauthSchedulingRateMultiplier float64) openAILegacyUpstreamRateOrder {
	rates := make(map[int64]float64, len(accounts))
	var first float64
	distinct := false
	for _, account := range accounts {
		if account == nil {
			continue
		}
		// 上游自报倍率只影响 OpenAI 平台的低倍率优先级；其他平台仍按本地倍率结算。
		if !account.IsOpenAIApiKey() && !account.IsOpenAIOAuth() {
			continue
		}
		rate, ok := openAISchedulingRate(account, now, oauthSchedulingRateMultiplier)
		if !ok {
			continue
		}
		if len(rates) == 0 {
			first = rate
		} else if rate != first {
			distinct = true
		}
		rates[account.ID] = rate
	}
	return openAILegacyUpstreamRateOrder{enabled: len(rates) >= 2 && distinct, rates: rates}
}

func openAISchedulingRate(account *Account, now time.Time, oauthSchedulingRateMultiplier float64) (float64, bool) {
	if account != nil && account.IsOpenAIOAuth() {
		return oauthSchedulingRateMultiplier, true
	}
	return openAIFreshUpstreamBillingRate(account, now)
}

func (o openAILegacyUpstreamRateOrder) compare(a, b *Account) int {
	if !o.enabled || a == nil || b == nil {
		return 0
	}
	aRate, aKnown := o.rates[a.ID]
	bRate, bKnown := o.rates[b.ID]
	if aKnown != bKnown {
		if aKnown {
			return -1
		}
		return 1
	}
	if !aKnown || aRate == bRate {
		return 0
	}
	if aRate < bRate {
		return -1
	}
	return 1
}

func openAIFreshUpstreamBillingRate(account *Account, now time.Time) (float64, bool) {
	if !isUpstreamBillingProbeAccount(account) {
		return 0, false
	}
	snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
	if snapshot == nil || (snapshot.Status != UpstreamBillingProbeStatusOK && snapshot.Status != UpstreamBillingProbeStatusFailed) ||
		snapshot.ReceivedAt == nil || snapshot.ReceivedAt.IsZero() {
		return 0, false
	}
	receivedAt := *snapshot.ReceivedAt
	freshUntil := snapshot.FreshUntil
	if freshUntil == nil && snapshot.Status == UpstreamBillingProbeStatusOK {
		interval := snapshot.NextProbeAt.Sub(receivedAt)
		if interval > 0 {
			freshUntil = probeTimePtr(receivedAt.Add(2 * interval))
		}
	}
	if freshUntil == nil || !freshUntil.After(receivedAt) || now.Before(receivedAt) || now.After(*freshUntil) {
		return 0, false
	}
	return upstreamBillingRateAt(snapshot.Data, now)
}
