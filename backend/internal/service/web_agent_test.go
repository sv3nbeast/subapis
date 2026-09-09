package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type agentRuntimeStub struct{}

func (agentRuntimeStub) GetWebChatRuntime(context.Context) WebChatRuntime {
	return WebChatRuntime{Enabled: true}
}

type agentKeyStub struct{}

func (agentKeyStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	return []Group{{ID: 7, Platform: PlatformOpenAI}}, nil
}
func (agentKeyStub) GenerateKey() (string, error) { panic("must not mint keys while enqueueing") }

type agentCatalogStub struct{}

func (agentCatalogStub) ListDisplayModelsForGroup(context.Context, int64, string) []SupportedModel {
	return []SupportedModel{{Name: "test-model"}}
}

type agentChatStub struct{ WebChatRepository }

func (agentChatStub) GetSession(_ context.Context, userID, id int64) (*WebChatSession, error) {
	if userID != 1 || id != 2 {
		return nil, ErrWebChatSessionNotFound
	}
	return &WebChatSession{ID: 2, UserID: 1, GroupID: 7, Model: "test-model", MaxOutputTokens: 100}, nil
}
func (agentChatStub) RecentMessages(context.Context, int64, int64, int) ([]WebChatMessage, error) {
	return []WebChatMessage{}, nil
}
func agentTestChat() *WebChatService {
	return NewWebChatService(agentChatStub{}, nil, agentKeyStub{}, agentCatalogStub{}, agentRuntimeStub{})
}

type agentExecutorFunc func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error)

func (f agentExecutorFunc) Execute(ctx context.Context, t *WebAgentTask, p func(string, string) error) (*WebAgentArtifact, error) {
	return f(ctx, t, p)
}
func (f agentExecutorFunc) Discard(context.Context, *WebAgentArtifact) error { return nil }

func agentTestArtifact() *WebAgentArtifact {
	return &WebAgentArtifact{Kind: "document", Title: "test", Filename: "test.docx", MIME: webAgentArtifactTypes["document"].mime,
		BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.docx", PreviewKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.pdf",
		SizeBytes: 10, PreviewBytes: 10, SHA256: strings.Repeat("a", 64), Spec: json.RawMessage(`{"kind":"document","title":"test"}`)}
}

type agentTaskStub struct {
	WebAgentRepository
	WebAgentArtifactRepository
	mu       sync.Mutex
	task     *WebAgentTask
	finished string
	claimed  bool
	events   []string
}

func (r *agentTaskStub) PublishArtifact(_ context.Context, _ *WebAgentTask, a *WebAgentArtifact) (*WebAgentArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.Status == WebAgentCancelRequested {
		r.finished = WebAgentCancelled
		return nil, nil
	}
	r.finished = WebAgentSucceeded
	return a, nil
}

func (r *agentTaskStub) CreateTask(_ context.Context, t *WebAgentTask) (*WebAgentTask, error) {
	r.task = t
	t.ID = 11
	return t, nil
}
func (r *agentTaskStub) ExpireTasks(context.Context) error { return nil }
func (r *agentTaskStub) ClaimTask(_ context.Context, token string, _ time.Duration) (*WebAgentTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed || r.task == nil {
		return nil, nil
	}
	r.claimed = true
	r.task.Status = WebAgentRunning
	r.task.LeaseToken = token
	copy := *r.task
	return &copy, nil
}
func (r *agentTaskStub) RenewTaskLease(context.Context, int64, string, time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.Status != WebAgentRunning {
		return ErrWebAgentLeaseLost
	}
	return nil
}
func (r *agentTaskStub) AppendTaskStep(_ context.Context, _ int64, _ string, kind string, _ json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, kind)
	return nil
}
func (r *agentTaskStub) FinishTask(_ context.Context, _ int64, _ string, status string, _ json.RawMessage, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.Status == WebAgentCancelRequested {
		status = WebAgentCancelled
	}
	r.finished = status
	return nil
}
func TestWebAgentRequestValidationAndStableIdentity(t *testing.T) {
	req := WebAgentCreateRequest{Kind: "slides", Prompt: " hello ", IdempotencyKey: "request-123", DocumentIDs: []int64{3, 1, 3}}
	normalized, err := normalizeWebAgentRequest(req)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, normalized.DocumentIDs)
	other := req
	other.Prompt = "hello"
	other.DocumentIDs = []int64{3, 1}
	other, err = normalizeWebAgentRequest(other)
	require.NoError(t, err)
	require.Equal(t, webAgentRequestHash(2, normalized), webAgentRequestHash(2, other))
	other.DocumentIDs = []int64{1, 3}
	require.NotEqual(t, webAgentRequestHash(2, normalized), webAgentRequestHash(2, other))
	for _, bad := range []WebAgentCreateRequest{
		{Kind: "shell", Prompt: "hello", IdempotencyKey: "request-123"},
		{Kind: "slides", Prompt: strings.Repeat("文", 20001), IdempotencyKey: "request-123"},
		{Kind: "slides", Prompt: "hello", IdempotencyKey: "short"},
		{Kind: "slides", Prompt: "hello", IdempotencyKey: "request-123", DocumentIDs: []int64{-1}},
	} {
		_, err = normalizeWebAgentRequest(bad)
		require.ErrorIs(t, err, ErrWebAgentInvalid)
	}
}
func TestWebAgentCreateFailsClosedAndFreezesContext(t *testing.T) {
	repo := &agentTaskStub{}
	chat := agentTestChat()
	svc := NewWebAgentService(repo, chat, nil)
	req := WebAgentCreateRequest{Kind: "document", Prompt: "Write a report", IdempotencyKey: "request-123"}
	_, err := svc.Create(context.Background(), 1, 2, req)
	require.ErrorIs(t, err, ErrWebAgentUnavailable)
	svc.executor = agentExecutorFunc(func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
		return agentTestArtifact(), nil
	})
	_, err = svc.Create(context.Background(), 1, 2, req)
	require.ErrorIs(t, err, ErrWebAgentUnavailable, "configured but stopped workers must not accept jobs")
	svc.running.Store(true) // Simulate a started worker without concurrent execution in this admission test.
	task, err := svc.Create(context.Background(), 1, 2, req)
	require.NoError(t, err)
	require.Equal(t, "test-model", task.Model)
	require.Contains(t, string(task.SessionSnapshot), `"session"`)
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "session_snapshot")
	require.NotContains(t, string(raw), "idempotency_key")
	_, err = svc.Create(context.Background(), 99, 2, req)
	require.ErrorIs(t, err, ErrWebChatSessionNotFound)
}
func TestWebAgentWorkerHasOneTerminalAndNoReplay(t *testing.T) {
	group := int64(7)
	repo := &agentTaskStub{task: &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model", Kind: "document", DeadlineAt: time.Now().Add(time.Minute)}}
	calls := 0
	svc := NewWebAgentService(repo, agentTestChat(), agentExecutorFunc(func(ctx context.Context, _ *WebAgentTask, p func(string, string) error) (*WebAgentArtifact, error) {
		calls++
		require.NoError(t, p("started", "render"))
		require.NoError(t, p("completed", "render"))
		return agentTestArtifact(), nil
	}))
	did, err := svc.RunOnce(context.Background())
	require.True(t, did)
	require.NoError(t, err)
	did, err = svc.RunOnce(context.Background())
	require.False(t, did)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, WebAgentSucceeded, repo.finished)
	require.Equal(t, []string{"step.started", "step.completed"}, repo.events)
}
func TestWebAgentWorkerCancelsWhenLeaseRevoked(t *testing.T) {
	group := int64(7)
	repo := &agentTaskStub{task: &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model", Kind: "document", DeadlineAt: time.Now().Add(time.Minute)}}
	entered := make(chan struct{})
	done := make(chan error, 1)
	svc := NewWebAgentService(repo, agentTestChat(), agentExecutorFunc(func(ctx context.Context, _ *WebAgentTask, _ func(string, string) error) (*WebAgentArtifact, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	svc.heartbeat = 5 * time.Millisecond
	go func() { _, err := svc.RunOnce(context.Background()); done <- err }()
	<-entered
	repo.mu.Lock()
	repo.task.Status = WebAgentCancelRequested
	repo.mu.Unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("executor did not receive cancellation")
	}
	require.Equal(t, WebAgentCancelled, repo.finished)
}
func TestWebAgentExecutorRejectsFakeOrIncompleteProgress(t *testing.T) {
	repo := &agentTaskStub{}
	for _, f := range []agentExecutorFunc{
		func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
			return nil, nil
		},
		func(_ context.Context, _ *WebAgentTask, p func(string, string) error) (*WebAgentArtifact, error) {
			_ = p("completed", "never started")
			return agentTestArtifact(), nil
		},
		func(_ context.Context, _ *WebAgentTask, p func(string, string) error) (*WebAgentArtifact, error) {
			_ = p("started", "unfinished")
			return agentTestArtifact(), nil
		},
		func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
			panic("boom")
		},
	} {
		svc := NewWebAgentService(repo, agentTestChat(), f)
		_, err := svc.execute(context.Background(), &WebAgentTask{ID: 11})
		require.Error(t, err)
	}
}

func TestWebAgentStartedWorkerStopsWithoutReplaying(t *testing.T) {
	group := int64(7)
	repo := &agentTaskStub{task: &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model", Kind: "document", DeadlineAt: time.Now().Add(time.Minute)}}
	entered := make(chan struct{})
	svc := NewWebAgentService(repo, agentTestChat(), agentExecutorFunc(func(ctx context.Context, _ *WebAgentTask, _ func(string, string) error) (*WebAgentArtifact, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	require.False(t, svc.Ready(context.Background()))
	svc.Start()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	require.True(t, svc.Ready(context.Background()))
	svc.Stop()
	require.False(t, svc.Ready(context.Background()))
	require.Equal(t, WebAgentInterrupted, repo.finished)
	svc.Start()
	require.False(t, svc.Ready(context.Background()), "a stopped runtime must be explicitly rebuilt, not replay old work")
}

type agentAlternateCatalog struct{}

func (agentAlternateCatalog) ListDisplayModelsForGroup(context.Context, int64, string) []SupportedModel {
	return []SupportedModel{{Name: "test-model"}, {Name: "alternate-model"}}
}
func TestWebAgentTaskFreezesExplicitModelWithoutChangingConversation(t *testing.T) {
	repo := &agentTaskStub{}
	chat := NewWebChatService(agentChatStub{}, nil, agentKeyStub{}, agentAlternateCatalog{}, agentRuntimeStub{})
	svc := NewWebAgentService(repo, chat, agentExecutorFunc(func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
		return agentTestArtifact(), nil
	}))
	svc.running.Store(true)
	group := int64(7)
	task, err := svc.Create(context.Background(), 1, 2, WebAgentCreateRequest{Kind: "document", Prompt: "report", Model: "alternate-model", GroupID: &group, IdempotencyKey: "explicit-model-selection"})
	require.NoError(t, err)
	require.Equal(t, "alternate-model", task.Model)
	require.Contains(t, string(task.SessionSnapshot), `"model":"alternate-model"`)
	session, err := chat.GetSession(context.Background(), 1, 2)
	require.NoError(t, err)
	require.Equal(t, "test-model", session.Model)
}
