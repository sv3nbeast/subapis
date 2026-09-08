package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type agentPlannerKeys struct {
	agentKeyStub
	generated int
}

func (k *agentPlannerKeys) GenerateKey() (string, error) {
	k.generated++
	return "candidate-local-key", nil
}

type agentPlannerKeyRepo struct{ userID int64 }

func (r agentPlannerKeyRepo) EnsureWebChatKey(_ context.Context, userID, groupID int64, _, _ string) (*APIKey, bool, error) {
	if r.userID != 0 {
		userID = r.userID
	}
	return &APIKey{UserID: userID, GroupID: &groupID, Key: "stable-owned-key"}, false, nil
}

type agentCallerFunc func(context.Context, *WebChatSession, *APIKey, []OpenAIChatMessage) (*WebAgentModelOutput, error)

func (f agentCallerFunc) Generate(ctx context.Context, s *WebChatSession, k *APIKey, m []OpenAIChatMessage) (*WebAgentModelOutput, error) {
	return f(ctx, s, k, m)
}
func agentPlannerTask(t *testing.T) *WebAgentTask {
	t.Helper()
	session, err := agentTestChat().GetSession(context.Background(), 1, 2)
	require.NoError(t, err)
	snapshot, err := json.Marshal(map[string]any{"session": session, "messages": []OpenAIChatMessage{{Role: "user", Content: "prior context"}}})
	require.NoError(t, err)
	return &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &session.GroupID, Model: session.Model, Kind: "document", Prompt: "生成报告", SessionSnapshot: snapshot}
}
func TestWebAgentPlannerUsesFrozenContextAndOwnedIdentity(t *testing.T) {
	keys := &agentPlannerKeys{}
	chat := NewWebChatService(agentChatStub{}, agentPlannerKeyRepo{}, keys, agentCatalogStub{}, agentRuntimeStub{})
	task := agentPlannerTask(t)
	sourceID := int64(9)
	task.SourceArtifactID = &sourceID
	source := agentTestArtifact()
	source.ID = 9
	source.UserID = 1
	source.SessionID = 2
	calls := 0
	planner := NewWebAgentModelPlanner(chat, agentCallerFunc(func(_ context.Context, s *WebChatSession, k *APIKey, m []OpenAIChatMessage) (*WebAgentModelOutput, error) {
		calls++
		require.Equal(t, int64(1), k.UserID)
		require.Equal(t, int64(7), *k.GroupID)
		require.Equal(t, "stable-owned-key", k.Key)
		require.Equal(t, "test-model", s.Model)
		require.Equal(t, 100, s.MaxOutputTokens)
		require.Equal(t, "prior context", m[0].Content)
		var input map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(m[1].Content), &input))
		require.JSONEq(t, string(source.Spec), string(input["source_version"]))
		require.Contains(t, s.SystemPrompt, "data-only renderer")
		return &WebAgentModelOutput{Content: "```json\n{\"kind\":\"document\",\"title\":\"报告\",\"sections\":[{\"heading\":\"摘要\",\"paragraphs\":[\"内容\"]}]}\n```", Generation: &WebAgentGeneration{RequestID: "owned-request"}}, nil
	}))
	plan, err := planner.Plan(context.Background(), task, source)
	require.NoError(t, err)
	require.True(t, json.Valid(plan.Spec))
	require.Equal(t, 1, calls)
	require.Equal(t, 1, keys.generated)
	require.Equal(t, "owned-request", plan.Generation.RequestID)
	require.NotContains(t, string(task.SessionSnapshot), "data-only renderer", "planning must not mutate the frozen snapshot")
}
func TestWebAgentPlannerRejectsInvalidContextBeforeModelCall(t *testing.T) {
	for _, kind := range []string{"owner", "source", "budget", "credential"} {
		t.Run(kind, func(t *testing.T) {
			keys := &agentPlannerKeys{}
			keyRepo := agentPlannerKeyRepo{}
			task := agentPlannerTask(t)
			switch kind {
			case "owner":
				task.UserID = 2
			case "source":
				id := int64(9)
				task.SourceArtifactID = &id
			case "budget":
				task.Prompt = strings.Repeat("x", webAgentModelMaxInputBytes)
			case "credential":
				keyRepo.userID = 2
			}
			chat := NewWebChatService(agentChatStub{}, keyRepo, keys, agentCatalogStub{}, agentRuntimeStub{})
			calls := 0
			planner := NewWebAgentModelPlanner(chat, agentCallerFunc(func(context.Context, *WebChatSession, *APIKey, []OpenAIChatMessage) (*WebAgentModelOutput, error) {
				calls++
				return nil, nil
			}))
			_, err := planner.Plan(context.Background(), task, nil)
			require.Error(t, err)
			require.Zero(t, calls)
			if kind != "credential" {
				require.Zero(t, keys.generated)
			}
		})
	}
}
func TestWebAgentPlannerNeverRepairsOrReplaysInvalidOutput(t *testing.T) {
	for _, content := range []string{`{"kind":"document",`, `I generated your file`, `{"kind":"slides","title":"wrong kind"}`, `{}`} {
		keys := &agentPlannerKeys{}
		chat := NewWebChatService(agentChatStub{}, agentPlannerKeyRepo{}, keys, agentCatalogStub{}, agentRuntimeStub{})
		calls := 0
		planner := NewWebAgentModelPlanner(chat, agentCallerFunc(func(context.Context, *WebChatSession, *APIKey, []OpenAIChatMessage) (*WebAgentModelOutput, error) {
			calls++
			return &WebAgentModelOutput{Content: content, Generation: &WebAgentGeneration{RequestID: "failed-spec-request"}}, nil
		}))
		plan, err := planner.Plan(context.Background(), agentPlannerTask(t), nil)
		require.Error(t, err)
		require.Equal(t, 1, calls)
		require.Empty(t, plan.Spec)
		require.Equal(t, "failed-spec-request", plan.Generation.RequestID)
	}
}

type agentPlannerDocs struct {
	WebChatDocumentRepository
	doc      *WebChatDocument
	searched bool
}

func (r *agentPlannerDocs) GetDocument(context.Context, int64, int64) (*WebChatDocument, error) {
	return r.doc, nil
}
func (r *agentPlannerDocs) SearchDocumentChunks(context.Context, int64, int64, []int64, string, int) ([]WebChatDocumentChunk, error) {
	r.searched = true
	return nil, errors.New("must not search unavailable documents")
}
func TestWebAgentPlannerRevalidatesAttachmentReadinessAndScope(t *testing.T) {
	for _, mode := range []string{"disabled", "wrong-session", "wrong-user"} {
		t.Run(mode, func(t *testing.T) {
			sessionID := int64(2)
			doc := &WebChatDocument{ID: 10, UserID: 1, SessionID: &sessionID, Status: WebChatDocumentStatusReady, Enabled: true}
			switch mode {
			case "disabled":
				doc.Enabled = false
			case "wrong-session":
				sessionID = 99
			case "wrong-user":
				doc.UserID = 2
			}
			repo := &agentPlannerDocs{doc: doc}
			settings := newWebChatDocumentSettingsTestDouble()
			settings.values[SettingKeyWebChatFilesEnabled] = "true"
			chat := NewWebChatService(agentChatStub{}, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, agentRuntimeStub{})
			chat.SetDocumentService(NewWebChatDocumentService(repo, settings, nil, nil))
			planner := NewWebAgentModelPlanner(chat, agentCallerFunc(func(context.Context, *WebChatSession, *APIKey, []OpenAIChatMessage) (*WebAgentModelOutput, error) {
				t.Error("must not call a model")
				return nil, nil
			}))
			task := agentPlannerTask(t)
			task.DocumentIDs = []int64{10}
			_, err := planner.Plan(context.Background(), task, nil)
			require.Error(t, err)
			require.False(t, repo.searched)
		})
	}
}
