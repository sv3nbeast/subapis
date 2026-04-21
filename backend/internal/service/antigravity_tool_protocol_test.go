package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClaudeToolProtocolForAntigravity_DowngradesOrphanToolResult(t *testing.T) {
	req := &antigravity.ClaudeRequest{
		Model: "claude-opus-4-6",
		Messages: []antigravity.ClaudeMessage{
			{
				Role:    "user",
				Content: []byte(`[{"type":"tool_result","tool_use_id":"tool_1","content":"ok"}]`),
			},
		},
	}

	changed, err := normalizeClaudeToolProtocolForAntigravity(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `[{"type":"text","text":"(tool_result) tool_use_id=tool_1\nok"}]`, string(req.Messages[0].Content))
}

func TestNormalizeClaudeToolProtocolForAntigravity_PreservesMatchedAndDowngradesMissing(t *testing.T) {
	req := &antigravity.ClaudeRequest{
		Model: "claude-opus-4-6",
		Messages: []antigravity.ClaudeMessage{
			{
				Role: "assistant",
				Content: []byte(`[
					{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"ls"},"signature":"sig_1"},
					{"type":"tool_use","id":"tool_2","name":"Read","input":{"file_path":"a.txt"},"signature":"sig_2"}
				]`),
			},
			{
				Role: "user",
				Content: []byte(`[
					{"type":"tool_result","tool_use_id":"tool_1","content":"done"},
					{"type":"tool_result","tool_use_id":"tool_999","content":"orphan"}
				]`),
			},
		},
	}

	changed, err := normalizeClaudeToolProtocolForAntigravity(req)
	require.NoError(t, err)
	require.True(t, changed)

	require.JSONEq(t, `[
		{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"ls"},"signature":"sig_1"},
		{"type":"text","text":"(tool_use) name=Read id=tool_2 input={\"file_path\":\"a.txt\"}"}
	]`, string(req.Messages[0].Content))
	require.JSONEq(t, `[
		{"type":"tool_result","tool_use_id":"tool_1","content":"done"},
		{"type":"text","text":"(tool_result) tool_use_id=tool_999\norphan"}
	]`, string(req.Messages[1].Content))
}

func TestAntigravityGatewayService_CachesToolSignaturesFromClaudeStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newAntigravityTestService(&config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		defer func() { _ = pw.Close() }()
		fmt.Fprintln(pw, `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Bash","id":"tool_1","args":{"command":"ls"}},"thoughtSignature":"sig_stream_1"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}}`)
		fmt.Fprintln(pw, "")
	}()

	result, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-opus-4-6", 123, "conv-1", nil)
	_ = pr.Close()

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, map[string]string{"tool_1": "sig_stream_1"}, svc.getClaudeToolUseSignatures(123, "conv-1"))
}

func TestAntigravityToolSignatureCache_PersistsAcrossReload(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "antigravity_tool_signatures.json")

	cache := newAntigravityToolSignatureCacheWithPath(cachePath)
	cache.remember(123, "conv-1", map[string]string{"tool_1": "sig_disk_1"}, time.Now())

	reloaded := newAntigravityToolSignatureCacheWithPath(cachePath)
	require.Equal(t, map[string]string{"tool_1": "sig_disk_1"}, reloaded.snapshot(123, "conv-1", time.Now()))
}
