package main

import (
	"net/http"
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
