//go:build integration

package repository

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type pipelineChatRepo struct{ service.WebChatRepository }
type pipelineApplicationRepo struct {
	pipelineChatRepo
	service.WebAgentRepository
	service.WebAgentArtifactRepository
	service.WebAgentStorageRepository
}

func (pipelineChatRepo) GetSession(_ context.Context, user, id int64) (*service.WebChatSession, error) {
	if user != 1 || id != 2 {
		return nil, service.ErrWebChatSessionNotFound
	}
	return &service.WebChatSession{ID: 2, UserID: 1, GroupID: 7, Platform: service.PlatformOpenAI, Model: "test-model", MaxOutputTokens: 4096}, nil
}
func (pipelineChatRepo) RecentMessages(context.Context, int64, int64, int) ([]service.WebChatMessage, error) {
	return []service.WebChatMessage{}, nil
}

type pipelineKeys struct{}

func (pipelineKeys) GetAvailableGroups(context.Context, int64) ([]service.Group, error) {
	return []service.Group{{ID: 7, Name: "Local synthetic fixture", Platform: service.PlatformOpenAI}}, nil
}
func (pipelineKeys) GenerateKey() (string, error) { return "local-candidate", nil }
func (pipelineKeys) EnsureWebChatKey(_ context.Context, user, group int64, _, _ string) (*service.APIKey, bool, error) {
	return &service.APIKey{Key: "local-owned-key", UserID: user, GroupID: &group}, false, nil
}

type pipelineCatalog struct{}

func (pipelineCatalog) ListDisplayModelsForGroup(context.Context, int64, string) []service.SupportedModel {
	return []service.SupportedModel{{Name: "test-model"}}
}

type pipelineSettings struct{}

func (pipelineSettings) GetWebChatRuntime(context.Context) service.WebChatRuntime {
	return service.WebChatRuntime{Enabled: true}
}

// This test uses a synthetic model HTTP endpoint, the actual Office worker,
// native files, a private blob store and real PostgreSQL task/artifact commits.
// It makes no paid model calls and may only target loopback test services.
func TestWebAgentRealOfficePipelineAndRevision(t *testing.T) {
	endpoint, token := os.Getenv("WEB_AGENT_OFFICE_ENDPOINT"), os.Getenv("WEB_AGENT_RENDERER_TOKEN")
	if endpoint == "" || token == "" {
		t.Skip("enable the local private renderer pipeline test")
	}
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost"}, u.Hostname())
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer readyCancel()
	for {
		request, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, endpoint+"/health", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, e := (&http.Client{Timeout: time.Second}).Do(request)
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		select {
		case <-readyCtx.Done():
			t.Fatal("local renderer was not ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
	tasks, db := agentIntegrationRepo(t)
	artifacts := NewWebChatRepository(db).(service.WebAgentArtifactRepository)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		n := calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-owned-key" {
			http.Error(w, "wrong fixture identity", 400)
			return
		}
		var body struct {
			Messages []service.OpenAIChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) < 2 {
			http.Error(w, "bad fixture request", 400)
			return
		}
		var input struct {
			Kind    string          `json:"kind"`
			Request string          `json:"request"`
			Source  json.RawMessage `json:"source_version"`
		}
		if json.Unmarshal([]byte(body.Messages[len(body.Messages)-1].Content), &input) != nil {
			http.Error(w, "bad fixture context", 400)
			return
		}
		spec := map[string]any{"kind": input.Kind, "title": "任务执行集成测试"}
		marker := "这是本地测试，不是外部模型输出。"
		switch input.Kind {
		case "document":
			spec["sections"] = []any{map[string]any{"heading": "测试内容", "paragraphs": []string{marker}}}
		case "slides":
			spec["slides"] = []any{map[string]any{"layout": "cover", "title": "任务执行集成测试", "subtitle": marker}}
		case "spreadsheet":
			spec["sheets"] = []any{map[string]any{"name": "测试数据", "columns": []any{map[string]any{"name": "测试说明"}}, "rows": [][]string{{marker}}}}
		default:
			http.Error(w, "invalid fixture kind", 400)
			return
		}
		if len(input.Source) > 0 {
			if json.Unmarshal(input.Source, &spec) != nil {
				http.Error(w, "invalid revision", 400)
				return
			}
			spec["title"] = "修订后的任务执行集成测试"
		}
		encoded, _ := json.Marshal(spec)
		w.Header().Set("X-Request-Id", fmt.Sprintf("synthetic-generation-%d", n))
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(encoded)}}}, "usage": map[string]int{"prompt_tokens": 50, "completion_tokens": 50}})
	}))
	t.Cleanup(gateway.Close)
	u, err = url.Parse(gateway.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	repo := &pipelineApplicationRepo{WebAgentRepository: tasks, WebAgentArtifactRepository: artifacts, WebAgentStorageRepository: NewWebChatRepository(db).(service.WebAgentStorageRepository)}
	chat := service.NewWebChatService(repo, pipelineKeys{}, pipelineKeys{}, pipelineCatalog{}, pipelineSettings{})
	require.NoError(t, chat.ConfigureAgent(config.WebAgentConfig{Enabled: true, RendererURL: endpoint, RendererToken: token, StoragePath: filepath.Join(t.TempDir(), "private-artifacts")}, port))
	t.Cleanup(chat.StopAgent)
	worker := chat.Agent()
	require.Eventually(t, func() bool { return worker.Ready(context.Background()) }, 5*time.Second, 10*time.Millisecond)
	readerService := chat.Artifacts()
	for kindIndex, kind := range []string{"document", "slides", "spreadsheet"} {
		var previous *service.WebAgentArtifact
		for i := 0; i < 2; i++ {
			input := service.WebAgentCreateRequest{Kind: kind, Prompt: "生成中文测试文件", IdempotencyKey: fmt.Sprintf("pipeline-%s-request-%d", kind, i)}
			if previous != nil {
				input.SourceArtifactID = &previous.ID
				input.Prompt = "只修改文件元数据标题，保留正文"
			}
			task, err := worker.Create(context.Background(), 1, 2, input)
			require.NoError(t, err)
			deadline := time.Now().Add(30 * time.Second)
			for !task.Terminal() && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
				task, err = tasks.GetTask(context.Background(), 1, task.ID)
				require.NoError(t, err)
			}
			require.Equal(t, service.WebAgentSucceeded, task.Status, "code=%s", task.ErrorCode)
			var result struct {
				ArtifactID int64                      `json:"artifact_id"`
				Generation service.WebAgentGeneration `json:"generation"`
			}
			require.NoError(t, json.Unmarshal(task.Result, &result))
			require.Equal(t, fmt.Sprintf("synthetic-generation-%d", kindIndex*2+i+1), result.Generation.RequestID)
			a, reader, _, err := readerService.Download(context.Background(), 1, result.ArtifactID, false)
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			reader.Close()
			require.NoError(t, err)
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, err)
			var document string
			for _, part := range archive.File {
				if strings.HasSuffix(part.Name, ".xml") {
					r, e := part.Open()
					require.NoError(t, e)
					content, e := io.ReadAll(r)
					r.Close()
					require.NoError(t, e)
					document += string(content)
				}
			}
			require.Contains(t, document, a.Title)
			require.Contains(t, document, "不是外部模型输出")
			_, preview, _, err := readerService.Download(context.Background(), 1, a.ID, true)
			require.NoError(t, err)
			pdf, err := io.ReadAll(preview)
			preview.Close()
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")))
			_, _, _, err = readerService.Download(context.Background(), 2, a.ID, false)
			require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
			require.Equal(t, i+1, a.Version)
			require.Equal(t, kind, a.Kind)
			if previous != nil {
				require.Equal(t, previous.LineageID, a.LineageID)
				require.Equal(t, &previous.ID, a.ParentID)
				require.NotEqual(t, previous.SHA256, a.SHA256)
				old, err := artifacts.GetArtifact(context.Background(), 1, previous.ID)
				require.NoError(t, err)
				require.Equal(t, "任务执行集成测试", old.Title)
			}
			require.NotContains(t, string(task.Result), "blob_key")
			require.NotContains(t, string(task.Result), "spec")
			previous = a
		}
		versions, err := artifacts.ArtifactVersions(context.Background(), 1, previous.ID, 0)
		require.NoError(t, err)
		require.Len(t, versions, 2)
		// All blob keys are generated UUID names, never a model-selected path.
		require.False(t, strings.Contains(previous.BlobKey, "/"))
	}
	require.Equal(t, int32(6), calls.Load(), "one generation per user action, no hidden replay")
}
