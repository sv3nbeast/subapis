package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const upstreamTerminalTestProviderMessage = "Our servers are currently overloaded. Please try again later."

func newUpstreamTerminalTestContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, rec
}

func capacityUpstreamTerminalError() *service.OpenAIUpstreamTerminalError {
	return &service.OpenAIUpstreamTerminalError{
		ClientStatus:    http.StatusServiceUnavailable,
		ErrType:         "server_error",
		Code:            "server_error",
		Message:         service.OpenAIUpstreamCapacityClientMessage(upstreamTerminalTestProviderMessage),
		UpstreamStatus:  http.StatusBadGateway,
		UpstreamMessage: upstreamTerminalTestProviderMessage,
	}
}

// Nothing written yet: the client gets one JSON envelope with the attributed
// message, and ops keeps the provider's own wording.
func TestRenderOpenAIUpstreamTerminalError_JSONBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/responses")

	(&OpenAIGatewayHandler{}).renderOpenAIUpstreamTerminalError(c, capacityUpstreamTerminalError(), false)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "server_error", gjson.Get(body, "error.type").String())
	require.Equal(t, "server_error", gjson.Get(body, "error.code").String())
	message := gjson.Get(body, "error.message").String()
	require.Contains(t, message, "OpenAI 官方服务当前算力过载")
	require.Contains(t, message, "not this platform")
	require.Contains(t, message, upstreamTerminalTestProviderMessage)
	require.NotContains(t, body, "server_is_overloaded")
	require.Equal(t, 1, strings.Count(body, `"error"`), "exactly one envelope")

	recorded, ok := c.Get(service.OpsUpstreamErrorMessageKey)
	require.True(t, ok)
	require.Equal(t, upstreamTerminalTestProviderMessage, recorded)
}

// The relayed upstream error event already reached the client: the Responses
// terminal must be exactly one response.failed carrying the attribution.
func TestRenderOpenAIUpstreamTerminalError_ResponsesFailedAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/responses")
	_, err := c.Writer.WriteString(`data: {"type":"error","error":{"code":"server_error","message":"relayed"}}` + "\n\n")
	require.NoError(t, err)

	(&OpenAIGatewayHandler{}).renderOpenAIUpstreamTerminalError(c, capacityUpstreamTerminalError(), false)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: response.failed"))
	require.Equal(t, 1, strings.Count(body, `"type":"response.failed"`))
	failed := body[strings.Index(body, `{"type":"response.failed"`):]
	require.Equal(t, "server_error", gjson.Get(failed, "response.error.code").String())
	require.Contains(t, gjson.Get(failed, "response.error.message").String(), "OpenAI 官方服务当前算力过载")
	require.Contains(t, gjson.Get(failed, "response.error.message").String(), upstreamTerminalTestProviderMessage)
	require.NotContains(t, body, "Upstream request failed")

	streamErrors := service.GetOpsStreamErrors(c)
	require.Len(t, streamErrors, 1)
	require.Equal(t, http.StatusServiceUnavailable, streamErrors[0].IntendedStatus)
	require.Contains(t, streamErrors[0].Message, upstreamTerminalTestProviderMessage)
	require.Equal(t, upstreamTerminalTestProviderMessage, streamErrors[0].UpstreamMessage)
}

// Chat Completions inbound uses its own in-band error frame.
func TestRenderOpenAIUpstreamTerminalError_ChatErrorEventAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/chat/completions")
	_, err := c.Writer.WriteString(": keepalive\n\n")
	require.NoError(t, err)

	(&OpenAIGatewayHandler{}).renderOpenAIUpstreamTerminalError(c, capacityUpstreamTerminalError(), false)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: error"))
	require.NotContains(t, body, "response.failed")
	event := body[strings.Index(body, `{"error"`):]
	require.Equal(t, "server_error", gjson.Get(event, "error.type").String())
	require.Equal(t, "server_error", gjson.Get(event, "error.code").String())
	require.Contains(t, gjson.Get(event, "error.message").String(), upstreamTerminalTestProviderMessage)
}

// Non-capacity terminals keep the mapped status and the provider message; no
// attribution is invented for them.
func TestRenderOpenAIUpstreamTerminalError_NonCapacityKeepsUpstreamSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/responses")

	(&OpenAIGatewayHandler{}).renderOpenAIUpstreamTerminalError(c, &service.OpenAIUpstreamTerminalError{
		ClientStatus:    http.StatusBadRequest,
		ErrType:         "upstream_error",
		Message:         "previous response could not be used",
		UpstreamStatus:  http.StatusBadRequest,
		UpstreamMessage: "previous response could not be used",
	}, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
	require.Equal(t, "previous response could not be used", gjson.Get(body, "error.message").String())
	require.NotContains(t, body, "OpenAI 官方服务当前算力过载")
}

// Failover exhaustion on a capacity body that lacks the request-scoped marker
// must still be attributed to OpenAI instead of the generic 502 mapping.
func TestOpenAIHandleFailoverExhausted_CapacityBodyWithoutMarkerIsAttributed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/responses")

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(`{"error":{"code":"server_is_overloaded","type":"service_unavailable_error","message":"` + upstreamTerminalTestProviderMessage + `"}}`),
	}, false)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "server_error", gjson.Get(body, "error.type").String())
	require.Contains(t, gjson.Get(body, "error.message").String(), "OpenAI 官方服务当前算力过载")
	require.Contains(t, gjson.Get(body, "error.message").String(), upstreamTerminalTestProviderMessage)
	require.NotContains(t, body, "Upstream service temporarily unavailable")
	require.NotContains(t, body, "server_is_overloaded")
}

// The generic mapping stays in place for non-capacity exhaustion.
func TestOpenAIHandleFailoverExhausted_NonCapacityKeepsGenericMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newUpstreamTerminalTestContext(t, "/v1/responses")

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`<html>bad gateway</html>`),
	}, false)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "Upstream service temporarily unavailable", gjson.Get(rec.Body.String(), "error.message").String())
}

// A gateway-synthesized terminal must not overwrite the provider message the
// service already recorded; the terminal text is only a fallback.
func TestApplyOpsStreamFailureFieldsKeepsRecordedUpstreamMessage(t *testing.T) {
	recorded := upstreamTerminalTestProviderMessage
	entry := &service.OpsInsertErrorLogInput{UpstreamErrorMessage: &recorded}

	applyOpsStreamFailureFields(entry, parsedOpsError{StreamFailure: true, Message: "Upstream request failed"}, http.StatusBadGateway)

	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, upstreamTerminalTestProviderMessage, *entry.UpstreamErrorMessage)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusBadGateway, *entry.UpstreamStatusCode)

	empty := &service.OpsInsertErrorLogInput{}
	applyOpsStreamFailureFields(empty, parsedOpsError{StreamFailure: true, Message: "official failure"}, http.StatusServiceUnavailable)
	require.NotNil(t, empty.UpstreamErrorMessage)
	require.Equal(t, "official failure", *empty.UpstreamErrorMessage)

	untouched := &service.OpsInsertErrorLogInput{}
	applyOpsStreamFailureFields(untouched, parsedOpsError{StreamFailure: false, Message: "ignored"}, http.StatusBadGateway)
	require.Nil(t, untouched.UpstreamErrorMessage)
	require.Nil(t, untouched.UpstreamStatusCode)
}

// A typed terminal the service already rendered as the JSON envelope counts as
// communicated, so no fallback frame follows it; an unrendered one does not.
func TestOpenAIForwardErrorAlreadyCommunicated_RecognizesRenderedUpstreamTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := newUpstreamTerminalTestContext(t, "/v1/responses")
	sizeBefore := c.Writer.Size()
	rendered := capacityUpstreamTerminalError()
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": rendered.ErrType, "code": rendered.Code, "message": rendered.Message}})
	rendered.Rendered = true

	require.True(t, openAIForwardErrorAlreadyCommunicated(c, sizeBefore, rendered))

	unrendered := capacityUpstreamTerminalError()
	require.False(t, openAIForwardErrorAlreadyCommunicated(c, sizeBefore, unrendered))
}
