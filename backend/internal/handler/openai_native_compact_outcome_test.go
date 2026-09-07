package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeCompactionCanceledAfterProgressIsNotSuccess(t *testing.T) {
	sink, restore := captureHandlerStructuredLog(t)
	defer restore()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(openAIRemoteCompactionV2Key, true)
	service.MarkOpenAINativeCompactionV2(c)
	_, err := c.Writer.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
	require.NoError(t, err)
	service.MarkOpsStreamError(c, "client_canceled", "Compaction canceled by downstream", 499)
	markOpenAIClientClosedRequest(c)
	(&OpenAIGatewayHandler{}).logOpenAIRemoteCompactOutcome(c, time.Now())
	require.Equal(t, http.StatusOK, c.Writer.Status(), "already-committed transport cannot change status")
	require.True(t, sink.ContainsMessageAtLevel("codex.remote_compact.failed", "warn"))
	require.False(t, sink.ContainsMessageAtLevel("codex.remote_compact.succeeded", "info"))
}
