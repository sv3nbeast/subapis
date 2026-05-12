package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAntigravityRequestLineageStore_NextRequestIDStablePerConversation(t *testing.T) {
	store := newAntigravityRequestLineageStore()

	first := store.nextRequestID(1, "conversation-a", mustParseTime(t, "2026-04-20T10:00:00+08:00"))
	second := store.nextRequestID(1, "conversation-a", mustParseTime(t, "2026-04-20T10:00:01+08:00"))
	third := store.nextRequestID(1, "conversation-b", mustParseTime(t, "2026-04-20T10:00:02+08:00"))

	firstParts := strings.Split(first, "/")
	secondParts := strings.Split(second, "/")
	thirdParts := strings.Split(third, "/")

	require.Len(t, firstParts, 5)
	require.Len(t, secondParts, 5)
	require.Len(t, thirdParts, 5)
	require.Equal(t, "agent", firstParts[0])
	require.Equal(t, firstParts[1], secondParts[1], "same conversation should reuse cascade id")
	require.Equal(t, firstParts[3], secondParts[3], "same conversation should reuse trajectory id")
	require.NotEqual(t, firstParts[1], thirdParts[1], "different conversation should get a new cascade id")
	require.NotEqual(t, firstParts[3], thirdParts[3], "different conversation should get a new trajectory id")
	require.Equal(t, "1", firstParts[4])
	require.Equal(t, "2", secondParts[4])
	require.Equal(t, "1", thirdParts[4])
}

func TestWrapV1InternalRequestWithIdentity_InjectsCloudCodeIdentity(t *testing.T) {
	svc := &AntigravityGatewayService{}
	innerBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)

	body, err := svc.wrapV1InternalRequestWithIdentity("project-1", "claude-opus-4-6-thinking", innerBody, antigravityRequestIdentity{
		SessionID: "session-42",
		RequestID: "agent/cascade-uuid/123/trajectory-uuid/9",
		UserAgent: "antigravity",
	})
	require.NoError(t, err)

	var wrapped antigravity.V1InternalRequest
	require.NoError(t, json.Unmarshal(body, &wrapped))
	require.Equal(t, "project-1", wrapped.Project)
	require.Equal(t, "agent/cascade-uuid/123/trajectory-uuid/9", wrapped.RequestID)
	require.Equal(t, "antigravity", wrapped.UserAgent)
	require.Equal(t, []string{"GOOGLE_ONE_AI"}, wrapped.EnabledCreditTypes)
	require.Equal(t, "session-42", wrapped.Request.SessionID)
}

func TestBuildCloudCodeRequestIdentity_UsesAccountSessionAndMetadataConversationKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(nil))
	req.Header.Set("session_id", "ignored-header-session")
	c.Request = req

	account := &Account{
		ID:    99,
		Extra: map[string]any{antigravityCloudCodeSessionIDExtraKey: "account-session-99"},
	}

	claudeReq := &antigravity.ClaudeRequest{
		Model: "claude-opus-4-6",
		Metadata: &antigravity.ClaudeMetadata{
			UserID: "metadata-session-abc",
		},
		Messages: []antigravity.ClaudeMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	svc := &AntigravityGatewayService{}
	first := svc.buildCloudCodeRequestIdentity(context.Background(), account, c, nil, claudeReq)
	second := svc.buildCloudCodeRequestIdentity(context.Background(), account, c, nil, claudeReq)

	require.Equal(t, "account-session-99", first.SessionID)
	require.Equal(t, "metadata-session-abc", first.ConversationKey)
	require.Equal(t, "antigravity", first.UserAgent)

	firstParts := strings.Split(first.RequestID, "/")
	secondParts := strings.Split(second.RequestID, "/")
	require.Len(t, firstParts, 5)
	require.Len(t, secondParts, 5)
	require.Equal(t, firstParts[1], secondParts[1], "same account + conversation should reuse cascade id")
	require.Equal(t, firstParts[3], secondParts[3], "same account + conversation should reuse trajectory id")
	require.Equal(t, "1", firstParts[4])
	require.Equal(t, "2", secondParts[4])
}

func mustParseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	return parsed
}
