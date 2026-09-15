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

// The capability an administrator declares must match what the model family can
// actually do. Chat Completions rejects image models before account selection,
// and the images endpoint rejects chat models, so a mismatch would only surface
// after a user sent a prompt.
func TestCatalogRejectsCapabilityMismatchedModels(t *testing.T) {
	svc, _, _ := chatCatalogFixture()
	svc.catalogGroups = chatCatalogGroupsStub{group: &Group{ID: 3, Platform: PlatformOpenAI, Status: StatusActive}}
	svc.channelService = webChatCatalogStub{modelsByGroup: map[int64][]SupportedModel{3: {{Name: "gpt-image-1.5"}, {Name: "gpt-5.5"}}}}

	entry := func(model string, capabilities ...string) WebChatCatalogConfig {
		return WebChatCatalogConfig{Entries: []WebChatCatalogEntry{{
			ID: "entry", Name: "Entry", GroupID: 3, Model: model, Enabled: true, Capabilities: capabilities,
		}}}
	}

	err := svc.ValidateModelCatalog(context.Background(), entry("gpt-image-1.5", WebChatCapabilityChat))
	require.ErrorContains(t, err, "must declare the image capability", "an image model on the chat path is refused upstream")

	err = svc.ValidateModelCatalog(context.Background(), entry("gpt-5.5", WebChatCapabilityImage))
	require.ErrorContains(t, err, "does not generate images")

	err = svc.ValidateModelCatalog(context.Background(), entry("gpt-5.5", "video"))
	require.ErrorContains(t, err, "unknown capability")

	require.NoError(t, svc.ValidateModelCatalog(context.Background(), entry("gpt-image-1.5", WebChatCapabilityImage)))
	require.NoError(t, svc.ValidateModelCatalog(context.Background(), entry("gpt-5.5", WebChatCapabilityChat)))
	require.NoError(t, svc.ValidateModelCatalog(context.Background(), entry("gpt-5.5")), "an entry with no declared capability stays a chat model")
}

// Catalogs published before image support carry no capability field. They must
// keep working and keep reporting themselves as chat models.
func TestCatalogWithoutCapabilitiesRemainsChat(t *testing.T) {
	entry := WebChatCatalogEntry{ID: "opus", Model: "claude-opus-5", Enabled: true}
	require.Equal(t, []string{WebChatCapabilityChat}, entry.capabilities())
	require.True(t, entry.supports(WebChatCapabilityChat))
	require.False(t, entry.supports(WebChatCapabilityImage))

	options := catalogOptions(&WebChatCatalogConfig{Entries: []WebChatCatalogEntry{entry}},
		[]WebChatGroupOption{{ID: 0, Models: []WebChatModelOption{{Name: "claude-opus-5"}}}})
	require.Len(t, options, 1)
	require.Equal(t, []string{WebChatCapabilityChat}, options[0].Capabilities, "the browser needs a capability to filter on")
}
