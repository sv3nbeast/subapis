package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
)

type antigravityBootstrapClientStub struct {
	loadResp       *antigravity.LoadCodeAssistResponse
	loadRaw        map[string]any
	loadErr        error
	modelsResp     *antigravity.FetchAvailableModelsResponse
	modelsErr      error
	userInfoResp   *antigravity.FetchUserInfoResponse
	userInfoErr    error
	onboardProject string
	onboardErr     error

	loadCalls     int
	modelsCalls   int
	userInfoCalls int
	onboardCalls  int
}

func (s *antigravityBootstrapClientStub) LoadCodeAssist(ctx context.Context, accessToken string) (*antigravity.LoadCodeAssistResponse, map[string]any, error) {
	s.loadCalls++
	return s.loadResp, s.loadRaw, s.loadErr
}

func (s *antigravityBootstrapClientStub) FetchAvailableModels(ctx context.Context, accessToken, projectID string) (*antigravity.FetchAvailableModelsResponse, map[string]any, error) {
	s.modelsCalls++
	return s.modelsResp, nil, s.modelsErr
}

func (s *antigravityBootstrapClientStub) FetchUserInfo(ctx context.Context, accessToken, projectID string) (*antigravity.FetchUserInfoResponse, error) {
	s.userInfoCalls++
	return s.userInfoResp, s.userInfoErr
}

func (s *antigravityBootstrapClientStub) OnboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	s.onboardCalls++
	return s.onboardProject, s.onboardErr
}

func TestAntigravityGatewayService_EnsureAntigravityBootstrapProbe_BackfillsProjectIDAndCaches(t *testing.T) {
	stub := &antigravityBootstrapClientStub{
		loadResp: &antigravity.LoadCodeAssistResponse{
			CloudAICompanionProject: "project-bootstrap-123",
			CurrentTier:             &antigravity.TierInfo{ID: "free-tier"},
		},
		modelsResp: &antigravity.FetchAvailableModelsResponse{
			Models: map[string]antigravity.ModelInfo{
				"claude-opus-4-6-thinking": {},
				"claude-sonnet-4-6":        {},
			},
		},
		userInfoResp: &antigravity.FetchUserInfoResponse{
			UserSettings: map[string]any{},
			RegionCode:   "US",
		},
	}

	svc := &AntigravityGatewayService{
		bootstrapProbeCache: newAntigravityBootstrapCache(),
		newAntigravityClient: func(proxyURL string) (antigravityBootstrapClient, error) {
			return stub, nil
		},
	}

	account := &Account{
		ID:          101,
		Platform:    PlatformAntigravity,
		Credentials: map[string]any{},
	}

	projectID := svc.ensureAntigravityBootstrapProbe(context.Background(), account, "token-1", "")
	require.Equal(t, "project-bootstrap-123", projectID)
	require.Equal(t, "project-bootstrap-123", account.GetCredential("project_id"))
	require.Equal(t, 1, stub.loadCalls)
	require.Equal(t, 1, stub.modelsCalls)
	require.Equal(t, 1, stub.userInfoCalls)

	projectID = svc.ensureAntigravityBootstrapProbe(context.Background(), account, "token-1", "")
	require.Equal(t, "project-bootstrap-123", projectID)
	require.Equal(t, 1, stub.loadCalls)
	require.Equal(t, 1, stub.modelsCalls)
	require.Equal(t, 1, stub.userInfoCalls)
}

func TestAntigravityGatewayService_EnsureAntigravityBootstrapProbe_OnboardsWhenLoadCodeAssistHasNoProject(t *testing.T) {
	stub := &antigravityBootstrapClientStub{
		loadResp: &antigravity.LoadCodeAssistResponse{
			CurrentTier: &antigravity.TierInfo{ID: "free-tier"},
		},
		loadRaw: map[string]any{
			"allowedTiers": []any{
				map[string]any{
					"id":        "free-tier",
					"isDefault": true,
				},
			},
		},
		onboardProject: "project-onboard-456",
		modelsResp: &antigravity.FetchAvailableModelsResponse{
			Models: map[string]antigravity.ModelInfo{
				"claude-opus-4-6-thinking": {},
			},
		},
		userInfoResp: &antigravity.FetchUserInfoResponse{RegionCode: "SG"},
	}

	svc := &AntigravityGatewayService{
		bootstrapProbeCache: newAntigravityBootstrapCache(),
		newAntigravityClient: func(proxyURL string) (antigravityBootstrapClient, error) {
			return stub, nil
		},
	}

	account := &Account{
		ID:          102,
		Platform:    PlatformAntigravity,
		Credentials: map[string]any{},
	}

	projectID := svc.ensureAntigravityBootstrapProbe(context.Background(), account, "token-2", "")
	require.Equal(t, "project-onboard-456", projectID)
	require.Equal(t, 1, stub.onboardCalls)
	require.Equal(t, "project-onboard-456", account.GetCredential("project_id"))
}
