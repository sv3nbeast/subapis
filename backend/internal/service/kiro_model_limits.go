package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

const (
	kiroModelLimitsCacheTTL        = 45 * time.Minute
	kiroModelLimitsFailureTTL      = time.Minute
	kiroModelLimitsLookupTimeout   = 5 * time.Second
	kiroMaxModelLimitsPages        = 20
	kiroContextWindowSourceDynamic = "upstream_model_metadata"
)

func newKiroModelLimitsCache() *gocache.Cache {
	return gocache.New(kiroModelLimitsCacheTTL, 10*time.Minute)
}

func (s *GatewayService) clearKiroModelLimitsFailure(account *Account) {
	if s == nil || s.kiroModelLimitsCache == nil || account == nil {
		return
	}
	region := strings.ToLower(strings.TrimSpace(kiroUpstreamModelsRegion(account)))
	if region == "" {
		region = kiroDefaultRegion
	}
	s.kiroModelLimitsCache.Delete(fmt.Sprintf("failure:%s:%d", region, account.ID))
}

// resolveKiroModelContextWindow reads the regional ListAvailableModels
// metadata. The gateway always retains the translator's static value as a
// failure-safe fallback, so an unavailable metadata endpoint cannot interrupt a
// previously working inference path.
func (s *GatewayService) resolveKiroModelContextWindow(ctx context.Context, account *Account, token, modelID string) (int, string) {
	if s == nil || s.httpUpstream == nil || s.kiroModelLimitsCache == nil || account == nil || strings.TrimSpace(token) == "" {
		return 0, ""
	}
	region := strings.ToLower(strings.TrimSpace(kiroUpstreamModelsRegion(account)))
	if region == "" {
		region = kiroDefaultRegion
	}
	failureKey := fmt.Sprintf("failure:%s:%d", region, account.ID)
	if cached, ok := s.kiroModelLimitsCache.Get(region); ok {
		if limits, valid := cached.(map[string]int); valid {
			return lookupKiroModelContextWindow(limits, modelID)
		}
	}
	if _, failedRecently := s.kiroModelLimitsCache.Get(failureKey); failedRecently {
		return 0, ""
	}

	result, err, _ := s.kiroModelLimitsSF.Do(region, func() (any, error) {
		if cached, ok := s.kiroModelLimitsCache.Get(region); ok {
			if limits, valid := cached.(map[string]int); valid {
				return limits, nil
			}
		}
		lookupCtx, cancel := context.WithTimeout(ctx, kiroModelLimitsLookupTimeout)
		defer cancel()
		limits, fetchErr := s.fetchKiroModelTokenLimits(lookupCtx, account, token)
		if fetchErr != nil {
			return nil, fetchErr
		}
		s.kiroModelLimitsCache.SetDefault(region, limits)
		return limits, nil
	})
	if err != nil {
		s.kiroModelLimitsCache.Set(failureKey, true, kiroModelLimitsFailureTTL)
		logger.L().Debug("kiro.model_limits_static_fallback",
			zap.Int64("account_id", account.ID),
			zap.String("region", region),
			zap.String("model", modelID),
			zap.Error(err),
		)
		return 0, ""
	}
	limits, ok := result.(map[string]int)
	if !ok {
		return 0, ""
	}
	s.kiroModelLimitsCache.Delete(failureKey)
	return lookupKiroModelContextWindow(limits, modelID)
}

func lookupKiroModelContextWindow(limits map[string]int, modelID string) (int, string) {
	for _, key := range kiroModelLimitLookupKeys(modelID) {
		if maxInputTokens := limits[key]; maxInputTokens > 0 {
			return maxInputTokens, kiroContextWindowSourceDynamic
		}
	}
	return 0, ""
}

func kiroModelLimitLookupKeys(modelID string) []string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	modelID = strings.TrimSuffix(modelID, "-thinking")
	modelID = strings.TrimSuffix(modelID, "-1m")
	modelID = strings.TrimSpace(strings.TrimSuffix(modelID, "[1m]"))
	keys := []string{modelID}
	if mapped := strings.ToLower(strings.TrimSpace(kiropkg.MapModel(modelID))); mapped != "" && mapped != modelID {
		keys = append(keys, mapped)
	}
	return keys
}

func (s *GatewayService) fetchKiroModelTokenLimits(ctx context.Context, account *Account, token string) (map[string]int, error) {
	limits := make(map[string]int)
	nextToken := ""
	seenTokens := make(map[string]struct{})
	for page := 0; page < kiroMaxModelLimitsPages; page++ {
		req, err := newKiroAvailableModelsRequest(ctx, account, token, nextToken)
		if err != nil {
			return nil, fmt.Errorf("build Kiro model limits request: %w", err)
		}
		tlsProfile := s.resolveKiroTLSProfile(account)
		resp, err := s.httpUpstream.DoWithTLS(req, kiroProxyURL(account), account.ID, account.Concurrency, tlsProfile)
		if err != nil {
			return nil, fmt.Errorf("request Kiro model limits: %w", err)
		}
		if resp == nil || resp.Body == nil {
			return nil, fmt.Errorf("Kiro model limits returned an empty response")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsBodyLimit+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Kiro model limits: %w", readErr)
		}
		if int64(len(body)) > upstreamModelsBodyLimit {
			return nil, fmt.Errorf("Kiro model limits response exceeds %d bytes", upstreamModelsBodyLimit)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("Kiro model limits returned HTTP %d", resp.StatusCode)
		}

		pageLimits, pageNextToken, err := extractKiroModelTokenLimitPage(body)
		if err != nil {
			return nil, err
		}
		for model, maxInputTokens := range pageLimits {
			limits[model] = maxInputTokens
		}
		pageNextToken = strings.TrimSpace(pageNextToken)
		if pageNextToken == "" {
			if len(limits) == 0 {
				return nil, fmt.Errorf("Kiro returned no model input-token limits")
			}
			return limits, nil
		}
		if _, duplicate := seenTokens[pageNextToken]; duplicate {
			return nil, fmt.Errorf("Kiro model limits pagination repeated nextToken")
		}
		seenTokens[pageNextToken] = struct{}{}
		nextToken = pageNextToken
	}
	return nil, fmt.Errorf("Kiro model limits pagination did not finish")
}

func extractKiroModelTokenLimitPage(body []byte) (map[string]int, string, error) {
	var response upstreamModelPage
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, "", fmt.Errorf("parse Kiro model limits: %w", err)
	}
	limits := make(map[string]int)
	entries := append(append(make([]upstreamModelEntry, 0, len(response.Data)+len(response.Models)), response.Data...), response.Models...)
	for _, entry := range entries {
		if entry.TokenLimits.MaxInputTokens <= 0 {
			continue
		}
		modelID := upstreamModelEntryID(entry)
		for _, key := range kiroModelLimitLookupKeys(modelID) {
			if key != "" {
				limits[key] = entry.TokenLimits.MaxInputTokens
			}
		}
	}
	return limits, strings.TrimSpace(response.NextToken), nil
}

func applyKiroContextWindowResolution(requestCtx *kiropkg.KiroRequestContext, contextWindowTokens int, source string) {
	if requestCtx == nil || contextWindowTokens <= 0 {
		return
	}
	requestCtx.ContextWindowTokens = contextWindowTokens
	requestCtx.ContextWindowSource = strings.TrimSpace(source)
	payloadEstimate := requestCtx.PayloadInputTokenEstimate
	if payloadEstimate <= 0 {
		payloadEstimate = requestCtx.InputTokenBudget
	}
	requestCtx.InputTokenBudget = payloadEstimate
	if requestCtx.InputTokenBudget > contextWindowTokens {
		requestCtx.InputTokenBudget = contextWindowTokens
	}
}
