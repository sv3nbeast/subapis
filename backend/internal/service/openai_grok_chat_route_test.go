package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func grokChatRouteContext(apiKeyID int64, platform, mode string, percent int, sessionID string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	c.Set("api_key", &APIKey{
		ID: apiKeyID,
		Group: &Group{
			Platform:                     platform,
			GrokChatUpstreamMode:         mode,
			GrokChatResponsesGrayPercent: percent,
		},
	})
	return c
}

func TestShouldUseGrokResponsesForChatModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hello"}]}`)

	require.False(t, shouldUseGrokResponsesForChat(grokChatRouteContext(1, PlatformGrok, GrokChatUpstreamModeRaw, 100, "session"), body))
	require.True(t, shouldUseGrokResponsesForChat(grokChatRouteContext(1, PlatformGrok, GrokChatUpstreamModeResponses, 0, "session"), body))
	require.False(t, shouldUseGrokResponsesForChat(grokChatRouteContext(1, PlatformGrok, GrokChatUpstreamModeGray, 0, "session"), body))
	require.True(t, shouldUseGrokResponsesForChat(grokChatRouteContext(1, PlatformGrok, GrokChatUpstreamModeGray, 100, "session"), body))
	require.False(t, shouldUseGrokResponsesForChat(grokChatRouteContext(1, PlatformOpenAI, GrokChatUpstreamModeResponses, 100, "session"), body))
}

func TestShouldUseGrokResponsesForChatGrayIsStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","messages":[{"role":"system","content":"code"},{"role":"user","content":"first turn"}]}`)
	c := grokChatRouteContext(17, PlatformGrok, GrokChatUpstreamModeGray, 50, "conversation-1")
	want := shouldUseGrokResponsesForChat(c, body)
	for i := 0; i < 20; i++ {
		require.Equal(t, want, shouldUseGrokResponsesForChat(c, body))
	}
}

func TestShouldUseGrokResponsesForChatGrayUsesAPIKeyIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","messages":[{"role":"user","content":"hello"}]}`)
	foundDifferent := false
	for apiKeyID := int64(1); apiKeyID < 1000; apiKeyID++ {
		first := shouldUseGrokResponsesForChat(grokChatRouteContext(apiKeyID, PlatformGrok, GrokChatUpstreamModeGray, 50, "shared-session"), body)
		second := shouldUseGrokResponsesForChat(grokChatRouteContext(apiKeyID+1000, PlatformGrok, GrokChatUpstreamModeGray, 50, "shared-session"), body)
		if first != second {
			foundDifferent = true
			break
		}
	}
	require.True(t, foundDifferent)
}

func TestShouldUseGrokResponsesForChatGrayFallsBackToContentSeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"grok","messages":[{"role":"system","content":"code"},{"role":"user","content":"first turn"},{"role":"assistant","content":"answer"},{"role":"user","content":"next"}]}`)
	c := grokChatRouteContext(9, PlatformGrok, GrokChatUpstreamModeGray, 50, "")
	want := shouldUseGrokResponsesForChat(c, body)
	require.Equal(t, want, shouldUseGrokResponsesForChat(c, body))
}
