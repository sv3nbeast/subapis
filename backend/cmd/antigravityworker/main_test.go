package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkerRuntime_ClientForReusesClientForSameExecutionShape(t *testing.T) {
	rt := &workerRuntime{}
	payload := executeRequest{
		Method:                       http.MethodPost,
		URL:                          "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent",
		AccountConcurrency:           2,
		ResponseHeaderTimeoutSeconds: 300,
	}

	client1, err := rt.clientFor(payload)
	require.NoError(t, err)
	client2, err := rt.clientFor(payload)
	require.NoError(t, err)
	require.Same(t, client1, client2)

	payload.AccountConcurrency = 3
	client3, err := rt.clientFor(payload)
	require.NoError(t, err)
	require.NotSame(t, client1, client3)
}

func TestWorkerClientCacheKeyNormalizesDefaults(t *testing.T) {
	require.Equal(
		t,
		workerClientCacheKey("", 0, nil, 0),
		workerClientCacheKey(" ", 1, nil, 300*time.Second),
	)
}

func TestHandleExecutePreservesChunkedTransfer(t *testing.T) {
	var gotTransferEncoding []string
	var gotContentLength int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTransferEncoding = append([]string(nil), r.TransferEncoding...)
		gotContentLength = r.ContentLength
		_, _ = w.Write([]byte(`ok`))
	}))
	defer upstream.Close()

	payload := executeRequest{
		Method:           http.MethodPost,
		URL:              upstream.URL,
		Headers:          http.Header{"Content-Type": []string{"application/json"}},
		BodyBase64:       base64.StdEncoding.EncodeToString([]byte(`{"enabledCreditTypes":["GOOGLE_ONE_AI"]}`)),
		TransferEncoding: []string{"chunked"},
		ContentLength:    -1,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handleExecute(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, []string{"chunked"}, gotTransferEncoding)
	require.Equal(t, int64(-1), gotContentLength)
}
