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
		&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "managed-key"}, "  a cat in a hat  ", nil)
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
		&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "k"}, "secret prompt text", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
	require.Contains(t, err.Error(), "content policy violation")
	require.False(t, strings.Contains(err.Error(), "secret prompt text"), "a task error must not echo the prompt")
}

// An edit must reach the edits endpoint carrying its source bytes. Posting to
// generations instead would silently ignore the input image and return an
// unrelated image the user still pays for.
func TestWebAgentImageClientSendsInputImagesToTheEditsEndpoint(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("source")...)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF}, []byte("second")...)
	for name, platform := range map[string]string{"openai": PlatformOpenAI, "grok": PlatformGrok} {
		t.Run(name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				w.Header().Set("Content-Type", "application/json")
				out := append([]byte("\x89PNG\r\n\x1a\n"), []byte("edited")...)
				_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(out) + `"}]}`))
			}))
			defer upstream.Close()
			client, err := NewWebAgentModelClient(8080)
			require.NoError(t, err)
			client.origin = upstream.URL

			out, err := client.GenerateImage(context.Background(),
				&WebChatSession{Model: "grok-imagine", Platform: platform}, &APIKey{Key: "k"}, "add a hat",
				[]WebAgentImageInput{{Data: png, MIME: "image/png"}, {Data: jpeg, MIME: "image/jpeg"}})
			require.NoError(t, err)
			require.Equal(t, "/v1/images/edits", gotPath)
			require.Equal(t, "image/png", out.MIME)

			// Bytes travel inline: a provider URL would expire before the artifact.
			wantFirst := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			if platform == PlatformGrok {
				// The Grok forwarder reads the canonical single-image field.
				first, ok := gotBody["image"].(map[string]any)
				require.True(t, ok, "grok reads image, not images[]")
				require.Equal(t, wantFirst, first["image_url"])
			} else {
				images, ok := gotBody["images"].([]any)
				require.True(t, ok, "openai edits read images[]")
				require.Len(t, images, 2, "every source image must be sent, not just the first")
				require.Equal(t, wantFirst, images[0].(map[string]any)["image_url"])
			}
		})
	}
}

// A text-to-image request must keep using the generations endpoint.
func TestWebAgentImageClientKeepsPromptOnlyRequestsOnGenerations(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("x")...)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`))
	}))
	defer upstream.Close()
	client, err := NewWebAgentModelClient(8080)
	require.NoError(t, err)
	client.origin = upstream.URL

	_, err = client.GenerateImage(context.Background(),
		&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "k"}, "a cat", nil)
	require.NoError(t, err)
	require.Equal(t, "/v1/images/generations", gotPath)
	require.NotContains(t, gotBody, "images")
	require.NotContains(t, gotBody, "image")
}

// An unusable input is refused before the request is sent: a dropped input would
// answer an unrelated image and still bill for it.
func TestWebAgentImageClientRefusesUnusableInputsBeforeSending(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer upstream.Close()
	client, err := NewWebAgentModelClient(8080)
	require.NoError(t, err)
	client.origin = upstream.URL

	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("s")...)
	for name, inputs := range map[string][]WebAgentImageInput{
		"empty bytes":        {{Data: nil, MIME: "image/png"}},
		"unsupported format": {{Data: png, MIME: "image/gif"}},
		"too many images":    {{Data: png, MIME: "image/png"}, {Data: png, MIME: "image/png"}, {Data: png, MIME: "image/png"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.GenerateImage(context.Background(),
				&WebChatSession{Model: "gpt-image-1.5"}, &APIKey{Key: "k"}, "edit", inputs)
			require.ErrorIs(t, err, ErrWebAgentInvalid)
		})
	}
	require.Zero(t, calls, "an unusable input must never reach the gateway")
}
