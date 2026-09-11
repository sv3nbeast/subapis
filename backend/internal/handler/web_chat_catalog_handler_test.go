package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type chatFirstDeltaWriter struct {
	bytes.Buffer
	first chan struct{}
}

func (w *chatFirstDeltaWriter) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if strings.Contains(w.Buffer.String(), "event: delta") {
		select {
		case w.first <- struct{}{}:
		default:
		}
	}
	return n, err
}

func TestWebChatCatalogAnthropicFirstDeltaBeforeTerminal(t *testing.T) {
	h := NewWebChatHandler(nil, nil, &config.Config{})
	reader, writer := io.Pipe()
	defer reader.Close()
	out := &chatFirstDeltaWriter{first: make(chan struct{}, 1)}
	h.httpClient = &http.Client{Transport: chatRouteTransport(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Contains(t, string(data), `"model":"claude-opus-5"`)
		require.Contains(t, string(data), "stable prefix")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
	})}
	finished := make(chan error, 1)
	go func() {
		_, err := h.forwardStreamingChat(context.Background(), out, "test-key", &service.WebChatSession{GroupID: 2, Model: "claude-opus-5", Platform: "anthropic", MaxOutputTokens: 8192}, []service.OpenAIChatMessage{{Role: "user", Content: "stable prefix"}})
		finished <- err
	}()
	_, err := io.WriteString(writer, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
	require.NoError(t, err)
	select {
	case <-out.first:
	case <-time.After(3 * time.Second):
		t.Fatal("first output waited for terminal event")
	}
	_, err = io.WriteString(writer, "data: {\"type\":\"message_stop\"}\n\n")
	require.NoError(t, err)
	_ = writer.Close()
	require.NoError(t, <-finished)
	require.Contains(t, out.String(), "hello")
}

type chatRouteRuntime struct{ cfg service.WebChatCatalogConfig }

func (s chatRouteRuntime) GetWebChatRuntime(context.Context) service.WebChatRuntime {
	return service.WebChatRuntime{Enabled: true, Catalog: &s.cfg}
}

type chatRouteKeys struct{}

func (chatRouteKeys) GetAvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{{ID: 2, Name: "internal", Platform: "openai", Status: "active"}}, nil
}
func (chatRouteKeys) GenerateKey() (string, error) { return "test-key", nil }
func (chatRouteKeys) EnsureWebChatKey(context.Context, int64, int64, string, string) (*service.APIKey, bool, error) {
	return &service.APIKey{Key: "test-key"}, false, nil
}

type chatRouteModels struct{}

func (chatRouteModels) ListDisplayModelsForGroup(context.Context, int64, string) []service.SupportedModel {
	return []service.SupportedModel{{Name: "gpt-6-astra"}}
}

type chatRouteRepo struct {
	service.WebChatRepository
	persisted service.WebChatUsage
	status    string
}

func (r *chatRouteRepo) GetSession(context.Context, int64, int64) (*service.WebChatSession, error) {
	return &service.WebChatSession{ID: 8, UserID: 7, GroupID: 2, Model: "gpt-6-astra", Platform: "openai", MaxOutputTokens: 8192}, nil
}
func (r *chatRouteRepo) CreateTurn(context.Context, int64, int64, string, string, *int64, ...service.WebChatTarget) (*service.WebChatMessage, *service.WebChatMessage, error) {
	return &service.WebChatMessage{ID: 1}, &service.WebChatMessage{ID: 2}, nil
}
func (r *chatRouteRepo) RecentMessages(context.Context, int64, int64, int) ([]service.WebChatMessage, error) {
	return []service.WebChatMessage{{Role: "user", Content: "prefix"}}, nil
}
func (r *chatRouteRepo) UpdateMessageResult(_ context.Context, _, _ int64, content, status, _, _ string, usage service.WebChatUsage) (*service.WebChatMessage, error) {
	r.persisted = usage
	r.status = status
	return &service.WebChatMessage{ID: 2, Content: content, Model: usage.Model}, nil
}

type chatRouteTransport func(*http.Request) (*http.Response, error)

func (f chatRouteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebChatCatalogSendHandlerRoutesAndSnapshotsWithoutReplay(t *testing.T) {
	for _, mode := range []string{"complete", "truncated", "upstream-error", "cancelled", "stale-selection"} {
		t.Run(mode, func(t *testing.T) {
			repo := &chatRouteRepo{}
			svc := service.NewWebChatService(repo, chatRouteKeys{}, chatRouteKeys{}, chatRouteModels{}, chatRouteRuntime{cfg: service.WebChatCatalogConfig{Entries: []service.WebChatCatalogEntry{{ID: "astra", Name: "GPT 6 Astra", GroupID: 2, Model: "gpt-6-astra", Enabled: true}}}})
			opts, err := svc.Options(context.Background(), 7)
			require.NoError(t, err)
			id := opts.Models[0].ID
			if mode == "stale-selection" {
				id = "stale"
			}
			h := NewWebChatHandler(svc, nil, &config.Config{})
			calls := 0
			h.httpClient = &http.Client{Transport: chatRouteTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), `"model":"gpt-6-astra"`)
				require.Contains(t, string(body), `"content":"prefix"`)
				if mode == "cancelled" {
					return nil, context.Canceled
				}
				code := 200
				stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
				if mode == "complete" {
					stream += "data: [DONE]\n\n"
				}
				if mode == "upstream-error" {
					code = 503
					stream = "unavailable"
				}
				return &http.Response{StatusCode: code, Header: http.Header{"X-Request-Id": []string{"test-request"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
			})}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
			c.Params = gin.Params{{Key: "id", Value: "8"}}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/web-chat/sessions/8/messages", strings.NewReader(`{"content":"hello","chat_model_id":"`+id+`","group_id":999,"model":"forged"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			h.SendMessage(c)
			if mode == "stale-selection" {
				require.Equal(t, 400, w.Code)
				require.Zero(t, calls)
				return
			}
			require.Equal(t, 1, calls, "no replay after output or generation")
			require.Equal(t, "gpt-6-astra", repo.persisted.Model)
			require.Equal(t, int64(2), repo.persisted.GroupID)
			if mode == "complete" {
				require.Equal(t, 1, strings.Count(w.Body.String(), "event: done"))
				require.NotContains(t, w.Body.String(), "event: error")
				require.Equal(t, "completed", repo.status)
			} else {
				require.Equal(t, 1, strings.Count(w.Body.String(), "event: error"))
				require.NotContains(t, w.Body.String(), "event: done")
				require.NotEqual(t, "completed", repo.status)
			}
		})
	}
}
