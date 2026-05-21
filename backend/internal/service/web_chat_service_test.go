package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type webChatRepoStub struct {
	recent []WebChatMessage
}

func (s *webChatRepoStub) CreateSession(context.Context, *WebChatSession) error {
	panic("unexpected CreateSession call")
}

func (s *webChatRepoStub) ListSessions(context.Context, int64) ([]WebChatSession, error) {
	panic("unexpected ListSessions call")
}

func (s *webChatRepoStub) GetSession(context.Context, int64, int64) (*WebChatSession, error) {
	panic("unexpected GetSession call")
}

func (s *webChatRepoStub) DeleteSession(context.Context, int64, int64) error {
	panic("unexpected DeleteSession call")
}

func (s *webChatRepoStub) CreateMessage(context.Context, *WebChatMessage) error {
	panic("unexpected CreateMessage call")
}

func (s *webChatRepoStub) UpdateMessageStatus(context.Context, int64, string, string, string) error {
	panic("unexpected UpdateMessageStatus call")
}

func (s *webChatRepoStub) TouchSession(context.Context, int64, string) error {
	panic("unexpected TouchSession call")
}

func (s *webChatRepoStub) ListMessages(context.Context, int64, int64) ([]WebChatMessage, error) {
	panic("unexpected ListMessages call")
}

func (s *webChatRepoStub) RecentMessages(context.Context, int64, int64, int) ([]WebChatMessage, error) {
	return s.recent, nil
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

	session, key, messages, assistant, err := svc.PrepareSend(context.Background(), 7, 88, "hello")

	require.ErrorIs(t, err, ErrWebChatDisabled)
	require.Nil(t, session)
	require.Nil(t, key)
	require.Nil(t, messages)
	require.Nil(t, assistant)
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
