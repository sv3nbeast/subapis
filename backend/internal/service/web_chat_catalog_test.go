package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type chatCatalogSettingsStub struct {
	cfg    WebChatCatalogConfig
	reads  int
	writes int
}

func (s *chatCatalogSettingsStub) GetWebChatRuntime(context.Context) WebChatRuntime {
	s.reads++
	return WebChatRuntime{Enabled: true, ProjectsEnabled: true, HistoryEnabled: true, Catalog: &s.cfg}
}
func (s *chatCatalogSettingsStub) SaveWebChatCatalog(_ context.Context, cfg WebChatCatalogConfig) error {
	s.cfg = cfg
	s.writes++
	return nil
}

type chatCatalogGroupsStub struct {
	GroupRepository
	group *Group
}

func (s chatCatalogGroupsStub) GetByID(context.Context, int64) (*Group, error) { return s.group, nil }

func chatCatalogFixture() (*WebChatService, *webChatRepoStub, *chatCatalogSettingsStub) {
	repo := &webChatRepoStub{session: &WebChatSession{ID: 88, UserID: 7, GroupID: 1, Model: "old"}, recent: []WebChatMessage{{Role: WebChatMessageRoleUser, Content: "unchanged prefix"}}}
	settings := &chatCatalogSettingsStub{cfg: WebChatCatalogConfig{Entries: []WebChatCatalogEntry{{ID: "opus", Name: "Claude Opus 5", Brand: "Claude", GroupID: 2, Model: "claude-opus-5", Enabled: true, Recommended: true}}}}
	svc := NewWebChatService(repo, webChatAPIKeyRepoStub{}, webChatAPIKeyManagerStub{groups: []Group{{ID: 2, Name: "private route", Platform: PlatformAnthropic, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}}}, webChatCatalogStub{modelsByGroup: map[int64][]SupportedModel{2: {{Name: "claude-opus-5"}, {Name: "not-in-catalog"}}}}, settings)
	svc.catalogGroups = chatCatalogGroupsStub{group: &Group{ID: 2, Platform: PlatformAnthropic, Status: StatusActive}}
	return svc, repo, settings
}

func TestWebChatCatalogOptionsDoNotExposeRoutes(t *testing.T) {
	svc, _, settings := chatCatalogFixture()
	settings.cfg.Entries = append(settings.cfg.Entries, WebChatCatalogEntry{ID: "hidden", Name: "Hidden", GroupID: 99, Model: "claude-opus-5", Enabled: true})
	opts, err := svc.Options(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, opts.Models, 1)
	data, err := json.Marshal(opts.Models)
	require.NoError(t, err)
	require.NotContains(t, string(data), "group_id")
	require.NotContains(t, string(data), "private route")
	require.Equal(t, SubscriptionTypeSubscription, opts.Models[0].BillingType)
}

func TestWebChatCatalogPrepareSendResolvesAndPreservesContext(t *testing.T) {
	svc, repo, settings := chatCatalogFixture()
	g, err := svc.PrepareSend(context.Background(), 7, 88, WebChatSendMessageRequest{ChatModelID: settings.cfg.Entries[0].selectionID(), Content: "next", GroupID: 99, Model: "forged"})
	require.NoError(t, err)
	require.Equal(t, int64(2), g.Session.GroupID)
	require.Equal(t, "claude-opus-5", g.Session.Model)
	require.Equal(t, "unchanged prefix", g.Messages[0].Content)
	require.Len(t, repo.created, 2)
	require.Equal(t, 1, settings.reads, "one runtime snapshot per turn; no repeated configuration I/O")
}

func TestWebChatCatalogRejectsStaleDisabledAndUnauthorizedSelections(t *testing.T) {
	for _, mode := range []string{"stale", "disabled", "unauthorized", "legacy-bypass", "branch-bypass"} {
		t.Run(mode, func(t *testing.T) {
			svc, repo, settings := chatCatalogFixture()
			id := settings.cfg.Entries[0].selectionID()
			req := WebChatSendMessageRequest{ChatModelID: id, Content: "next"}
			switch mode {
			case "stale":
				settings.cfg.Entries[0].Model = "replacement"
			case "disabled":
				settings.cfg.Entries[0].Enabled = false
			case "unauthorized":
				svc.apiKeyService = webChatAPIKeyManagerStub{}
			case "legacy-bypass":
				req.ChatModelID = ""
				req.GroupID = 2
				req.Model = "not-in-catalog"
			}
			var err error
			if mode == "branch-bypass" {
				_, err = svc.PrepareRegenerate(context.Background(), 7, 88, 1)
			} else {
				_, err = svc.PrepareSend(context.Background(), 7, 88, req)
			}
			require.Error(t, err)
			require.Empty(t, repo.created)
			require.Nil(t, repo.updatedTarget)
		})
	}
}

func TestWebChatCatalogValidation(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate", "missing-model", "cli-only", "inactive", "multiple-defaults", "bad-id"} {
		t.Run(mode, func(t *testing.T) {
			svc, _, settings := chatCatalogFixture()
			cfg := settings.cfg
			switch mode {
			case "duplicate":
				cfg.Entries = append(cfg.Entries, cfg.Entries[0])
			case "missing-model":
				cfg.Entries[0].Model = "made-up-model"
			case "cli-only":
				svc.catalogGroups = chatCatalogGroupsStub{group: &Group{ID: 2, Status: StatusActive, ClaudeCodeOnly: true}}
			case "inactive":
				svc.catalogGroups = chatCatalogGroupsStub{group: &Group{ID: 2, Status: "disabled"}}
			case "multiple-defaults":
				other := cfg.Entries[0]
				other.ID = "second"
				cfg.Entries = append(cfg.Entries, other)
			case "bad-id":
				cfg.Entries[0].ID = "../invalid"
			}
			err := svc.SaveModelCatalog(context.Background(), cfg)
			if mode == "valid" {
				require.NoError(t, err)
				require.Equal(t, 1, settings.writes)
			} else {
				require.Error(t, err)
				require.Zero(t, settings.writes)
			}
		})
	}
}

func TestWebChatCatalogProjectDefaultResolvedOnServer(t *testing.T) {
	svc, _, settings := chatCatalogFixture()
	in := WebChatProjectInput{Name: "Work", DefaultChatModelID: settings.cfg.Entries[0].selectionID(), DefaultModel: "forged"}
	require.NoError(t, svc.validateProjectInput(context.Background(), 7, &in))
	require.Equal(t, int64(2), *in.DefaultGroupID)
	require.Equal(t, "claude-opus-5", in.DefaultModel)
}

func TestWebChatCatalogPreservesLegacyContextAndKeyIdentity(t *testing.T) {
	svc, _, settings := chatCatalogFixture()
	legacy, err := svc.PrepareSend(context.Background(), 7, 88, WebChatSendMessageRequest{Content: "next", GroupID: 2, Model: "claude-opus-5"})
	require.NoError(t, err)
	catalog, err := svc.PrepareSend(context.Background(), 7, 88, WebChatSendMessageRequest{Content: "next", ChatModelID: settings.cfg.Entries[0].selectionID()})
	require.NoError(t, err)
	require.Equal(t, legacy.Messages, catalog.Messages)
	require.Equal(t, legacy.Session.GroupID, catalog.Session.GroupID)
	require.Equal(t, legacy.Session.Model, catalog.Session.Model)
	require.Equal(t, legacy.APIKey, catalog.APIKey)
}
