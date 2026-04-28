package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

var preferredAntigravityTestModelIDs = []string{
	"claude-sonnet-4-6",
	"claude-opus-4-6-thinking",
	"gemini-3.1-pro-high",
}

func DefaultAntigravityTestModelID() string {
	return preferredAntigravityTestModelIDs[0]
}

func PrioritizeAntigravityModels(models []antigravity.ClaudeModel) []antigravity.ClaudeModel {
	if len(models) <= 1 {
		return models
	}

	priority := make(map[string]int, len(preferredAntigravityTestModelIDs))
	for idx, id := range preferredAntigravityTestModelIDs {
		priority[id] = idx
	}

	out := append([]antigravity.ClaudeModel(nil), models...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iPreferred := priority[out[i].ID]
		pj, jPreferred := priority[out[j].ID]
		switch {
		case iPreferred && jPreferred:
			return pi < pj
		case iPreferred:
			return true
		case jPreferred:
			return false
		default:
			return false
		}
	})
	return out
}

func (s *AccountTestService) GetAntigravityAvailableModels(ctx context.Context, account *Account) ([]antigravity.ClaudeModel, error) {
	if s == nil || s.antigravityGatewayService == nil {
		return nil, fmt.Errorf("antigravity gateway service not configured")
	}
	return s.antigravityGatewayService.GetAvailableModelsForAccount(ctx, account)
}

func (s *AntigravityGatewayService) GetAvailableModelsForAccount(ctx context.Context, account *Account) ([]antigravity.ClaudeModel, error) {
	if s == nil {
		return nil, fmt.Errorf("antigravity gateway service not configured")
	}
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	if account.Platform != PlatformAntigravity {
		return nil, fmt.Errorf("not an antigravity account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("live antigravity model discovery requires oauth account")
	}
	if s.tokenProvider == nil {
		return nil, fmt.Errorf("antigravity token provider not configured")
	}

	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	if projectID == "" {
		projectID = strings.TrimSpace(s.ensureAntigravityBootstrapProbe(ctx, account, accessToken, proxyURL))
	}
	if projectID == "" {
		return nil, fmt.Errorf("project_id is empty after bootstrap")
	}

	client, err := s.bootstrapClientForAccount(account, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("antigravity bootstrap client: %w", err)
	}

	modelsResp, _, err := client.FetchAvailableModels(ctx, accessToken, projectID)
	if err != nil {
		return nil, fmt.Errorf("fetch available models: %w", err)
	}

	models := buildAntigravityModelsFromFetchResponse(modelsResp)
	if len(models) == 0 {
		return nil, fmt.Errorf("fetch available models returned empty model list")
	}
	return models, nil
}

func (s *AntigravityGatewayService) bootstrapClientForAccount(account *Account, proxyURL string) (antigravityBootstrapClient, error) {
	factory := s.newAntigravityClient
	if factory == nil {
		factory = defaultAntigravityBootstrapClientFactory
	}
	if worker := s.antigravityWorker(account); worker != nil {
		return worker.bootstrapClientFor(factory, proxyURL)
	}
	return factory(proxyURL)
}

func buildAntigravityModelsFromFetchResponse(resp *antigravity.FetchAvailableModelsResponse) []antigravity.ClaudeModel {
	if resp == nil || len(resp.Models) == 0 {
		return nil
	}

	defaultByID := make(map[string]antigravity.ClaudeModel)
	defaultOrder := make([]string, 0)
	for _, model := range antigravity.DefaultModels() {
		if _, exists := defaultByID[model.ID]; !exists {
			defaultOrder = append(defaultOrder, model.ID)
		}
		defaultByID[model.ID] = model
	}

	seen := make(map[string]struct{}, len(resp.Models))
	out := make([]antigravity.ClaudeModel, 0, len(resp.Models))
	add := func(id string, info antigravity.ModelInfo) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}

		model := defaultByID[id]
		if model.ID == "" {
			model = antigravity.ClaudeModel{
				ID:          id,
				Type:        "model",
				DisplayName: id,
			}
		}
		if displayName := strings.TrimSpace(info.DisplayName); displayName != "" {
			model.DisplayName = displayName
		}
		out = append(out, model)
	}

	for _, id := range preferredAntigravityTestModelIDs {
		if info, ok := resp.Models[id]; ok {
			add(id, info)
		}
	}
	for _, id := range defaultOrder {
		if info, ok := resp.Models[id]; ok {
			add(id, info)
		}
	}

	remaining := make([]string, 0, len(resp.Models))
	for id := range resp.Models {
		if _, exists := seen[id]; exists {
			continue
		}
		remaining = append(remaining, id)
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		add(id, resp.Models[id])
	}

	return out
}
