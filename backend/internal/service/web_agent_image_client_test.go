package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The workbench must reach the images endpoint, not chat completions: chat
// completions rejects image models before an account is even selected.
func TestWebAgentImageClientCallsTheImagesEndpoint(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("X-Request-Id", "img-req-1")
		w.Header().Set("Content-Type", "application/json")
		png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("bytes")...)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `"}],"usage":{"input_tokens":9,"output_tokens":0}}`))
	}))
	defer upstream.Close()

	client, err := NewWebAgentModelClient(8080)
	require.NoError(t, err)
	client.origin = upstream.URL

	out, err := client.GenerateImage(context.Background(),
		&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "managed-key"}, "  a cat in a hat  ")
	require.NoError(t, err)
	require.Equal(t, "/v1/images/generations", gotPath, "images must not be routed through chat completions")
	require.Equal(t, "Bearer managed-key", gotAuth)
	require.Equal(t, "gpt-image-1.5", gotBody["model"])
	require.Equal(t, "a cat in a hat", gotBody["prompt"], "the prompt is trimmed before it is sent")
	require.Equal(t, "b64_json", gotBody["response_format"], "a provider URL would expire before the artifact it backs")
	require.Equal(t, float64(1), gotBody["n"])
	require.Equal(t, "image/png", out.MIME)
	require.Equal(t, "img-req-1", out.Generation.RequestID, "the gateway request id links the artifact to its billing record")
	require.Equal(t, int64(9), out.Generation.Usage.InputTokens)
}

func TestWebAgentImageClientSurfacesUpstreamFailureWithoutLeakingThePrompt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"content policy violation"}}`))
	}))
	defer upstream.Close()
	client, err := NewWebAgentModelClient(8080)
	require.NoError(t, err)
	client.origin = upstream.URL

	_, err = client.GenerateImage(context.Background(),
		&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "k"}, "secret prompt text")
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
	require.Contains(t, err.Error(), "content policy violation")
	require.False(t, strings.Contains(err.Error(), "secret prompt text"), "a task error must not echo the prompt")
}
