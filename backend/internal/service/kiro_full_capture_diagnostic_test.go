package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroFullCaptureStoresBodyRoundTripAndRedactsCredentialsForTargetOnly(t *testing.T) {
	oldRoot := kiroFullCaptureRoot
	oldMax := kiroFullCaptureMaxSessions
	kiroFullCaptureRoot = t.TempDir()
	kiroFullCaptureMaxSessions = 4
	kiroFullCaptureSessions.Store(0)
	defer func() {
		kiroFullCaptureRoot = oldRoot
		kiroFullCaptureMaxSessions = oldMax
		kiroFullCaptureSessions.Store(0)
	}()

	groupID := int64(29)
	parsed := &ParsedRequest{
		Model:          "claude-opus-5",
		Stream:         true,
		GroupID:        &groupID,
		MetadataUserID: FormatMetadataUserID(strings.Repeat("a", 64), "", kiroFullCaptureTargetSessionID, "2.1.241"),
		SessionContext: &SessionContext{UserID: 1, APIKeyID: 119},
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("x-api-key", "client-secret-key")
	account := &Account{ID: 2597, Name: "capture-account"}
	clientBody := []byte(`{"model":"claude-opus-5","private":"client-body-secret"}`)

	ctx := maybeStartKiroFullCapture(context.Background(), c, account, parsed, clientBody)
	session := kiroFullCaptureFromContext(ctx)
	require.NotNil(t, session)

	nonTarget := *parsed
	nonTarget.Model = "claude-sonnet-5"
	require.Nil(t, kiroFullCaptureFromContext(maybeStartKiroFullCapture(context.Background(), c, account, &nonTarget, clientBody)))
	nonTarget = *parsed
	nonTarget.SessionContext = &SessionContext{UserID: 1, APIKeyID: 1}
	require.Nil(t, kiroFullCaptureFromContext(maybeStartKiroFullCapture(context.Background(), c, account, &nonTarget, clientBody)))
	nonTarget = *parsed
	nonTarget.MetadataUserID = FormatMetadataUserID(strings.Repeat("b", 64), "", "other-session", "2.1.241")
	require.Nil(t, kiroFullCaptureFromContext(maybeStartKiroFullCapture(context.Background(), c, account, &nonTarget, clientBody)))

	clientHeaders, err := os.ReadFile(filepath.Join(session.dir, "01_client_request_headers.json"))
	require.NoError(t, err)
	require.NotContains(t, string(clientHeaders), "client-secret-key")
	require.Contains(t, string(clientHeaders), `\u003credacted\u003e`)
	session.writeClientResponseHeaders(http.Header{"Set-Cookie": []string{"client-response-cookie-secret"}})
	clientResponseHeaders, err := os.ReadFile(filepath.Join(session.dir, "03_client_response_headers.json"))
	require.NoError(t, err)
	require.NotContains(t, string(clientResponseHeaders), "client-response-cookie-secret")
	require.Contains(t, string(clientResponseHeaders), `\u003credacted\u003e`)
	storedClientBody, err := os.ReadFile(filepath.Join(session.dir, "02_client_request_body.json"))
	require.NoError(t, err)
	require.Equal(t, clientBody, storedClientBody)

	req := httptest.NewRequest(http.MethodPost, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", nil)
	req.Header.Set("Authorization", "Bearer upstream-secret-token")
	req.Header.Set("Cookie", "upstream-cookie-secret")
	payload := []byte(`{"conversationState":{"private":"upstream-payload-secret"}}`)
	attempt := session.beginAttempt(account, "AmazonQ", req, payload)
	require.NotNil(t, attempt)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"x-amzn-requestid": []string{"provider-request-id"}, "set-cookie": []string{"provider-cookie-secret"}},
		Body:       io.NopCloser(bytes.NewReader([]byte("raw-eventstream-secret"))),
	}
	attempt.wrapResponse(resp)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, []byte("raw-eventstream-secret"), raw)

	requestHeaders, err := os.ReadFile(filepath.Join(attempt.dir, "01_upstream_request_headers.json"))
	require.NoError(t, err)
	require.NotContains(t, string(requestHeaders), "upstream-secret-token")
	require.NotContains(t, string(requestHeaders), "upstream-cookie-secret")
	require.Contains(t, string(requestHeaders), `\u003credacted\u003e`)
	storedPayload, err := os.ReadFile(filepath.Join(attempt.dir, "02_upstream_request_body.json"))
	require.NoError(t, err)
	require.Equal(t, payload, storedPayload)
	responseMetadata, err := os.ReadFile(filepath.Join(attempt.dir, "03_upstream_response.json"))
	require.NoError(t, err)
	require.NotContains(t, string(responseMetadata), "provider-cookie-secret")
	require.Contains(t, string(responseMetadata), `\u003credacted\u003e`)
	storedRaw, err := os.ReadFile(filepath.Join(attempt.dir, "04_upstream_response.eventstream"))
	require.NoError(t, err)
	require.Equal(t, raw, storedRaw)

	var downstream bytes.Buffer
	mirror, closer := session.newClientResponseMirror(&downstream)
	_, err = mirror.Write([]byte("client-sse-secret"))
	require.NoError(t, err)
	require.NoError(t, closer.Close())
	require.Equal(t, "client-sse-secret", downstream.String())
	storedDownstream, err := os.ReadFile(filepath.Join(session.dir, "04_client_response.sse"))
	require.NoError(t, err)
	require.Equal(t, []byte("client-sse-secret"), storedDownstream)

	var finalDownstream bytes.Buffer
	finalMirror, finalCloser := session.newClientResponseMirrorNamed(&finalDownstream, "05_client_response.sse")
	_, err = finalMirror.Write([]byte("final-client-sse-secret"))
	require.NoError(t, err)
	require.NoError(t, finalCloser.Close())
	storedFinalDownstream, err := os.ReadFile(filepath.Join(session.dir, "05_client_response.sse"))
	require.NoError(t, err)
	require.Equal(t, []byte("final-client-sse-secret"), storedFinalDownstream)

	info, err := os.Stat(filepath.Join(attempt.dir, "04_upstream_response.eventstream"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestKiroFullCaptureNianzsRouteCapturesTranslatedPayloadAndEventStream(t *testing.T) {
	oldRoot := kiroFullCaptureRoot
	oldMax := kiroFullCaptureMaxSessions
	kiroFullCaptureRoot = t.TempDir()
	kiroFullCaptureMaxSessions = 4
	kiroFullCaptureSessions.Store(0)
	defer func() {
		kiroFullCaptureRoot = oldRoot
		kiroFullCaptureMaxSessions = oldMax
		kiroFullCaptureSessions.Store(0)
	}()

	groupID := int64(29)
	body := []byte(`{"model":"claude-opus-5","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"capture route"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	parsed.GroupID = &groupID
	parsed.MetadataUserID = FormatMetadataUserID(strings.Repeat("c", 64), "", kiroFullCaptureTargetSessionID, "2.1.241")
	parsed.SessionContext = &SessionContext{UserID: 1, APIKeyID: 119}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("x-api-key", "route-client-secret")
	svc, _, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "captured route response", 31, 7))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	entries, err := os.ReadDir(kiroFullCaptureRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	sessionDir := filepath.Join(kiroFullCaptureRoot, entries[0].Name())
	attemptEntries, err := os.ReadDir(sessionDir)
	require.NoError(t, err)
	var attemptDir string
	for _, entry := range attemptEntries {
		if entry.IsDir() {
			attemptDir = filepath.Join(sessionDir, entry.Name())
			break
		}
	}
	require.NotEmpty(t, attemptDir)
	translatedPayload, err := os.ReadFile(filepath.Join(attemptDir, "02_upstream_request_body.json"))
	require.NoError(t, err)
	require.Contains(t, string(translatedPayload), "conversationState")
	rawEventStream, err := os.ReadFile(filepath.Join(attemptDir, "04_upstream_response.eventstream"))
	require.NoError(t, err)
	require.NotEmpty(t, rawEventStream)
	translatedSSE, err := os.ReadFile(filepath.Join(sessionDir, "04_translated_response.sse"))
	require.NoError(t, err)
	clientSSE, err := os.ReadFile(filepath.Join(sessionDir, "05_client_response.sse"))
	require.NoError(t, err)
	require.NotEqual(t, translatedSSE, clientSSE, "final gateway normalization must remain observable as a separate stage")
	require.Contains(t, string(translatedSSE), "_sub2api_kiro_usage_final")
	require.NotContains(t, string(clientSSE), "_sub2api_kiro_usage_final")
	var visible strings.Builder
	for _, event := range nianzsSSEPayloadsByType(string(clientSSE), "content_block_delta") {
		if event.Get("delta.type").String() == "text_delta" {
			visible.WriteString(event.Get("delta.text").String())
		}
	}
	require.Contains(t, visible.String(), "captured route response")
	require.Equal(t, 1, bytes.Count(translatedSSE, []byte("event: message_stop")))
	require.Equal(t, 1, bytes.Count(clientSSE, []byte("event: message_stop")))
}
