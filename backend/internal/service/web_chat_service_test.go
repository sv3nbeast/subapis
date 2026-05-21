package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type webChatRepoStub struct {
	session       *WebChatSession
	recent        []WebChatMessage
	created       []WebChatMessage
	updatedTarget *WebChatSession
	touchedTitle  string
}

func (s *webChatRepoStub) CreateSession(context.Context, *WebChatSession) error {
	panic("unexpected CreateSession call")
}

func (s *webChatRepoStub) ListSessions(context.Context, int64) ([]WebChatSession, error) {
	panic("unexpected ListSessions call")
}

func (s *webChatRepoStub) GetSession(context.Context, int64, int64) (*WebChatSession, error) {
	if s.session != nil {
		cp := *s.session
		return &cp, nil
	}
	panic("unexpected GetSession call")
}

func (s *webChatRepoStub) UpdateSessionTarget(_ context.Context, _ int64, _ int64, groupID int64, model string) error {
	s.updatedTarget = &WebChatSession{GroupID: groupID, Model: model}
	if s.session != nil {
		s.session.GroupID = groupID
		s.session.Model = model
	}
	return nil
}

func (s *webChatRepoStub) DeleteSession(context.Context, int64, int64) error {
	panic("unexpected DeleteSession call")
}

func (s *webChatRepoStub) CreateMessage(_ context.Context, message *WebChatMessage) error {
	cp := *message
	cp.ID = int64(len(s.created) + 1)
	s.created = append(s.created, cp)
	message.ID = cp.ID
	return nil
}

func (s *webChatRepoStub) UpdateMessageStatus(context.Context, int64, string, string, string) error {
	panic("unexpected UpdateMessageStatus call")
}

func (s *webChatRepoStub) TouchSession(_ context.Context, _ int64, title string) error {
	s.touchedTitle = title
	return nil
}

func (s *webChatRepoStub) ListMessages(context.Context, int64, int64) ([]WebChatMessage, error) {
	panic("unexpected ListMessages call")
}

func (s *webChatRepoStub) RecentMessages(context.Context, int64, int64, int) ([]WebChatMessage, error) {
	return s.recent, nil
}

type webChatRuntimeStub struct {
	enabled bool
}

func (s webChatRuntimeStub) GetWebChatRuntime(context.Context) WebChatRuntime {
	return WebChatRuntime{Enabled: s.enabled}
}

type webChatCatalogStub struct {
	modelsByGroup map[int64][]SupportedModel
}

func (s webChatCatalogStub) ListDisplayModelsForGroup(_ context.Context, groupID int64, _ string) []SupportedModel {
	return s.modelsByGroup[groupID]
}

type webChatAPIKeyRepoStub struct {
	key *APIKey
}

func (s webChatAPIKeyRepoStub) EnsureWebChatKey(context.Context, int64, int64, string, string) (*APIKey, bool, error) {
	if s.key != nil {
		return s.key, false, nil
	}
	return &APIKey{ID: 1, Key: "sk-web-chat"}, true, nil
}

type webChatAPIKeyManagerStub struct {
	groups []Group
}

func (s webChatAPIKeyManagerStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return s.groups, nil
}

func (s webChatAPIKeyManagerStub) GenerateKey() (string, error) {
	return "sk-generated-web-chat", nil
}

func TestWebChatService_OptionsDisabledFailClosed(t *testing.T) {
	svc := NewWebChatService(&webChatRepoStub{}, nil, nil, nil, nil)

	options, err := svc.Options(context.Background(), 7)

	require.NoError(t, err)
	require.False(t, options.Enabled)
	require.Empty(t, options.Groups)
	require.Nil(t, options.DefaultGroupID)
	require.Empty(t, options.DefaultModel)
}

func TestWebChatService_PrepareSendDisabledRejects(t *testing.T) {
	svc := NewWebChatService(&webChatRepoStub{}, nil, nil, nil, nil)

	session, key, messages, assistant, err := svc.PrepareSend(context.Background(), 7, 88, WebChatSendMessageRequest{Content: "hello"})

	require.ErrorIs(t, err, ErrWebChatDisabled)
	require.Nil(t, session)
	require.Nil(t, key)
	require.Nil(t, messages)
	require.Nil(t, assistant)
}

func TestWebChatService_OptionsUsesDisplayModels(t *testing.T) {
	one := 0.000005
	apiKeySvc := webChatAPIKeyManagerStub{
		groups: []Group{{ID: 2, Name: "claude-opus-4.6", Status: StatusActive, Platform: PlatformAnthropic}},
	}
	svc := NewWebChatService(&webChatRepoStub{}, nil, apiKeySvc, webChatCatalogStub{
		modelsByGroup: map[int64][]SupportedModel{
			2: {{
				Name:     "claude-opus-4-6",
				Platform: PlatformAnthropic,
				Pricing:  &ChannelModelPricing{BillingMode: BillingModeToken, InputPrice: &one},
			}},
		},
	}, webChatRuntimeStub{enabled: true})

	options, err := svc.Options(context.Background(), 7)

	require.NoError(t, err)
	require.True(t, options.Enabled)
	require.Len(t, options.Groups, 1)
	require.Equal(t, "claude-opus-4-6", options.Groups[0].Models[0].Name)
	require.NotNil(t, options.Groups[0].Models[0].Pricing)
	require.Equal(t, int64(2), *options.DefaultGroupID)
	require.Equal(t, "claude-opus-4-6", options.DefaultModel)
}

func TestWebChatService_PrepareSendUsesRequestedTarget(t *testing.T) {
	repo := &webChatRepoStub{
		session: &WebChatSession{ID: 88, UserID: 7, GroupID: 1, Model: "old-model"},
		recent:  []WebChatMessage{{Role: WebChatMessageRoleUser, Content: "hello", Status: WebChatMessageStatusCompleted}},
	}
	apiKeySvc := webChatAPIKeyManagerStub{
		groups: []Group{
			{ID: 1, Name: "old", Status: StatusActive, Platform: PlatformAnthropic},
			{ID: 2, Name: "new", Status: StatusActive, Platform: PlatformAnthropic},
		},
	}
	svc := NewWebChatService(repo, webChatAPIKeyRepoStub{}, apiKeySvc, webChatCatalogStub{
		modelsByGroup: map[int64][]SupportedModel{
			1: {{Name: "old-model", Platform: PlatformAnthropic}},
			2: {{Name: "new-model", Platform: PlatformAnthropic}},
		},
	}, webChatRuntimeStub{enabled: true})

	session, key, messages, assistant, err := svc.PrepareSend(context.Background(), 7, 88, WebChatSendMessageRequest{
		Content: "use new target",
		GroupID: 2,
		Model:   "new-model",
	})

	require.NoError(t, err)
	require.NotNil(t, key)
	require.NotNil(t, assistant)
	require.Equal(t, int64(2), session.GroupID)
	require.Equal(t, "new-model", session.Model)
	require.NotNil(t, repo.updatedTarget)
	require.Equal(t, int64(2), repo.updatedTarget.GroupID)
	require.Equal(t, "new-model", repo.updatedTarget.Model)
	require.Len(t, repo.created, 2)
	require.NotEmpty(t, messages)
}

func TestWebChatService_BuildContextMessagesCapsSingleOversizedMessage(t *testing.T) {
	content := strings.Repeat("你", webChatContextMaxChars+32)
	svc := NewWebChatService(&webChatRepoStub{
		recent: []WebChatMessage{{
			Role:    WebChatMessageRoleUser,
			Content: content,
			Status:  WebChatMessageStatusCompleted,
		}},
	}, nil, nil, nil, nil)

	messages, err := svc.buildContextMessages(context.Background(), 7, 88)

	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, WebChatMessageRoleUser, messages[0].Role)
	require.Equal(t, webChatContextMaxChars, len([]rune(messages[0].Content)))
}

func TestWebChatService_BuildContextMessagesDropsOlderMessagesOverCap(t *testing.T) {
	older := strings.Repeat("a", 100)
	newest := strings.Repeat("b", webChatContextMaxChars-10)
	svc := NewWebChatService(&webChatRepoStub{
		recent: []WebChatMessage{
			{Role: WebChatMessageRoleUser, Content: older, Status: WebChatMessageStatusCompleted},
			{Role: WebChatMessageRoleAssistant, Content: newest, Status: WebChatMessageStatusCompleted},
		},
	}, nil, nil, nil, nil)

	messages, err := svc.buildContextMessages(context.Background(), 7, 88)

	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, WebChatMessageRoleAssistant, messages[0].Role)
	require.Equal(t, newest, messages[0].Content)
}
