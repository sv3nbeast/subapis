package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func agentModelClientForServer(t *testing.T, s *httptest.Server) *WebAgentModelClient {
	t.Helper()
	u, err := url.Parse(s.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	client, err := NewWebAgentModelClient(port)
	require.NoError(t, err)
	return client
}
func agentModelSession(platform string) *WebChatSession {
	return &WebChatSession{Model: "test-model", Platform: platform, SystemPrompt: "stable instructions", MaxOutputTokens: 4096}
}

func TestWebAgentModelUsesOwnedGatewayProtocolAndNormalTerminal(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, "Bearer owned-test-key", r.Header.Get("Authorization"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				if platform == PlatformAnthropic {
					require.Equal(t, true, body["stream"])
					require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
				} else {
					require.Equal(t, false, body["stream"])
				}
				require.Equal(t, "test-model", body["model"])
				require.Equal(t, float64(4096), body["max_tokens"])
				w.Header().Set("X-Request-Id", "gateway-request-id")
				w.Header().Set("X-Client-Request-Id", "gateway-client-id")
				if platform == PlatformAnthropic {
					require.Equal(t, "/v1/messages", r.URL.Path)
					require.Equal(t, "stable instructions", body["system"])
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, agentAnthropicSSEFixture())
				} else {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"kind\":\"document\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":7}}}`)
				}
			}))
			defer s.Close()
			out, err := agentModelClientForServer(t, s).Generate(context.Background(), agentModelSession(platform), &APIKey{Key: "owned-test-key"}, []OpenAIChatMessage{{Role: "user", Content: "create a report"}})
			require.NoError(t, err)
			require.Equal(t, `{"kind":"document"}`, out.Content)
			require.Equal(t, "gateway-request-id", out.Generation.RequestID)
			require.Equal(t, "gateway-client-id", out.Generation.ClientRequestID)
			require.Equal(t, int64(7), out.Generation.Usage.CacheReadTokens)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestWebAgentModelRejectsTruncationAndUnexpectedTools(t *testing.T) {
	for _, tc := range []struct {
		body      string
		anthropic bool
	}{
		{`{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`, false},
		{`{"choices":[{"message":{"content":"{}"}}]}`, false},
		{`{"choices":[{"finish_reason":"stop","message":{"content":"{}","tool_calls":[{"id":"unexpected"}]}}]}`, false},
		{`{"choices":[{"finish_reason":"stop","message":{"content":"{}","refusal":"no"}}]}`, false},
		{`{"type":"message","stop_reason":"max_tokens","content":[{"type":"text","text":"{}"}]}`, true},
		{`{"type":"message","stop_reason":"end_turn","content":[{"type":"tool_use","name":"shell"}]}`, true},
		{`{"type":"message","content":[{"type":"text","text":"{}"}]}`, true},
		{`{"error":{"message":"private upstream detail"}}`, false},
		{`{"choices":[`, false},
	} {
		var out WebAgentModelOutput
		require.Error(t, parseWebAgentModelResponse([]byte(tc.body), tc.anthropic, &out), tc.body)
		require.Empty(t, out.Content)
	}
}

func TestWebAgentModelNoReplayOrCredentialRedirect(t *testing.T) {
	var calls, leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	_, err := agentModelClientForServer(t, s).Generate(context.Background(), agentModelSession(PlatformOpenAI), &APIKey{Key: "private-test-key"}, []OpenAIChatMessage{{Role: "user", Content: "report"}})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Zero(t, leaked.Load())
}

func TestWebAgentModelCancellationStopsTheOnlyRequest(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := agentModelClientForServer(t, s).Generate(ctx, agentModelSession(PlatformOpenAI), &APIKey{Key: "test"}, []OpenAIChatMessage{{Role: "user", Content: "report"}})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request was not started")
	}
	cancel()
	select {
	case err := <-done:
		require.True(t, errors.Is(err, context.Canceled), "%v", err)
	case <-time.After(time.Second):
		t.Fatal("model call ignored cancellation")
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestWebAgentModelRequestPreservesPrefixAndBounds(t *testing.T) {
	session := agentModelSession(PlatformAnthropic)
	first := []OpenAIChatMessage{{Role: "user", Content: "unchanged prompt"}}
	_, before, _, err := buildWebAgentModelRequest(session, first)
	require.NoError(t, err)
	_, after, _, err := buildWebAgentModelRequest(session, append(first, OpenAIChatMessage{Role: "assistant", Content: "prior answer"}, OpenAIChatMessage{Role: "user", Content: "continue"}))
	require.NoError(t, err)
	var a, b struct {
		System   string            `json:"system"`
		Messages []json.RawMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(before, &a))
	require.NoError(t, json.Unmarshal(after, &b))
	require.Equal(t, a.System, b.System)
	require.JSONEq(t, string(a.Messages[0]), string(b.Messages[0]))
	require.Contains(t, string(a.Messages[0]), `"ttl":"5m"`)
	_, _, _, err = buildWebAgentModelRequest(session, []OpenAIChatMessage{{Role: "user", Content: strings.Repeat("x", webAgentModelMaxInputBytes)}})
	require.Error(t, err)
	session.MaxOutputTokens = 32769
	_, _, _, err = buildWebAgentModelRequest(session, first)
	require.ErrorIs(t, err, ErrWebAgentInvalid)
}

func TestWebAgentModelDoesNotInventMissingUsage(t *testing.T) {
	var out WebAgentModelOutput
	require.NoError(t, parseWebAgentModelResponse([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`), false, &out))
	require.Nil(t, out.Generation.Usage)
	encoded, err := json.Marshal(out.Generation)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "usage")
}

func TestWebAgentModelTransferEOFIsNotACompletedGeneration(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "1000")
		w.Header().Set("X-Request-Id", "incomplete-transfer")
		fmt.Fprint(w, `{"choices":[`)
	}))
	defer s.Close()
	out, err := agentModelClientForServer(t, s).Generate(context.Background(), agentModelSession(PlatformOpenAI), &APIKey{Key: "test"}, []OpenAIChatMessage{{Role: "user", Content: "report"}})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "model_response_interrupted", webAgentFailureCode(err))
	require.Empty(t, out.Content)
	require.Equal(t, "incomplete-transfer", out.Generation.RequestID)
	require.Equal(t, int32(1), calls.Load())
}
func TestWebAgentModelInternalTimeoutIsDistinctFromCallerCancellation(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Drain the request so net/http can observe connection cancellation.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); s.Close() }()
	client := agentModelClientForServer(t, s)
	client.http.Timeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := client.Generate(ctx, agentModelSession(PlatformOpenAI), &APIKey{Key: "test"}, []OpenAIChatMessage{{Role: "user", Content: "report"}})
	require.Error(t, err)
	require.NoError(t, ctx.Err())
	require.Equal(t, "model_timeout", webAgentFailureCode(err))
	require.Equal(t, int32(1), calls.Load())
}
