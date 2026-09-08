package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Executors are trusted, allowlisted runtime adapters, not arbitrary user code.
// They must honor cancellation and report actual execution, never guessed progress.
type WebAgentExecutor interface {
	Execute(context.Context, *WebAgentTask, func(string, string) error) (json.RawMessage, error)
}
type WebAgentService struct {
	repo      WebAgentRepository
	chat      *WebChatService
	executor  WebAgentExecutor
	leaseTTL  time.Duration
	heartbeat time.Duration
	now       func() time.Time
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	running   atomic.Bool
}

func NewWebAgentService(repo WebAgentRepository, chat *WebChatService, executor WebAgentExecutor) *WebAgentService {
	return &WebAgentService{repo: repo, chat: chat, executor: executor, leaseTTL: 30 * time.Second, heartbeat: 5 * time.Second, now: time.Now}
}
func (s *WebAgentService) Ready(ctx context.Context) bool {
	return s.configured(ctx) && s.running.Load()
}
func (s *WebAgentService) configured(ctx context.Context) bool {
	return s != nil && s.repo != nil && s.chat != nil && s.chat.repo != nil &&
		s.chat.apiKeyService != nil && s.chat.channelService != nil &&
		s.executor != nil && s.chat.FeatureEnabled(ctx)
}
func (s *WebAgentService) readable(ctx context.Context) error {
	if s == nil || s.repo == nil || s.chat == nil {
		return ErrWebAgentUnavailable
	}
	if !s.chat.FeatureEnabled(ctx) {
		return ErrWebChatDisabled
	}
	return nil
}
func (s *WebAgentService) Create(ctx context.Context, userID, sessionID int64, input WebAgentCreateRequest) (*WebAgentTask, error) {
	if !s.Ready(ctx) {
		return nil, ErrWebAgentUnavailable
	}
	if userID <= 0 || sessionID <= 0 {
		return nil, ErrWebAgentInvalid
	}
	req, err := normalizeWebAgentRequest(input)
	if err != nil {
		return nil, err
	}
	session, err := s.chat.GetSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if _, _, err = s.chat.validateGroupModel(ctx, userID, session.GroupID, session.Model); err != nil {
		return nil, err
	}
	messages, err := s.chat.buildContextMessages(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(struct {
		Session  *WebChatSession     `json:"session"`
		Messages []OpenAIChatMessage `json:"messages"`
	}{session, messages})
	if err != nil {
		return nil, err
	}
	groupID := session.GroupID
	return s.repo.CreateTask(ctx, &WebAgentTask{UserID: userID, SessionID: sessionID, GroupID: &groupID,
		Model: session.Model, Kind: req.Kind, Prompt: req.Prompt, DocumentIDs: req.DocumentIDs,
		SessionSnapshot: snapshot, IdempotencyKey: req.IdempotencyKey, RequestHash: webAgentRequestHash(sessionID, req),
		Status: WebAgentQueued, DeadlineAt: s.now().Add(WebAgentTaskTimeout)})
}
func (s *WebAgentService) Get(ctx context.Context, userID, id int64) (*WebAgentTask, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	return s.repo.GetTask(ctx, userID, id)
}
func (s *WebAgentService) List(ctx context.Context, userID, sessionID, before int64) ([]WebAgentTask, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if userID <= 0 || sessionID < 0 || before < 0 {
		return nil, ErrWebAgentInvalid
	}
	if sessionID > 0 {
		if _, err := s.chat.GetSession(ctx, userID, sessionID); err != nil {
			return nil, err
		}
	}
	return s.repo.ListTasks(ctx, userID, sessionID, before)
}
func (s *WebAgentService) Events(ctx context.Context, userID, id, after int64) ([]WebAgentTaskEvent, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if userID <= 0 || id <= 0 || after < 0 {
		return nil, ErrWebAgentInvalid
	}
	return s.repo.ListTaskEvents(ctx, userID, id, after)
}
func (s *WebAgentService) Cancel(ctx context.Context, userID, id int64) (*WebAgentTask, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	return s.repo.CancelTask(ctx, userID, id)
}

func (s *WebAgentService) Start() {
	if s == nil || s.repo == nil || s.executor == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.running.Store(true)
	go func() {
		// Expiry must not wait behind a long-running task. It never requeues
		// interrupted execution and every sweep is bounded in the repository.
		cleanupDone := make(chan struct{})
		go func() {
			defer close(cleanupDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					expireCtx, expireCancel := context.WithTimeout(ctx, 5*time.Second)
					err := s.repo.ExpireTasks(expireCtx)
					expireCancel()
					if err != nil && ctx.Err() == nil {
						slog.Warn("web_agent.expiry_failed", "error", err)
					}
				}
			}
		}()
		defer func() { cancel(); <-cleanupDone; s.running.Store(false); close(s.done) }()
		for {
			if ctx.Err() != nil {
				return
			}
			didWork, err := s.RunOnce(ctx)
			if err != nil {
				slog.Warn("web_agent.worker_failed", "error", err)
			}
			if didWork && err == nil {
				continue
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}
func (s *WebAgentService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	s.running.Store(false)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("web_agent.shutdown_pending")
	}
}

// RunOnce is independent of any browser request context. A crashed/expired
// execution becomes interrupted; it is never silently replayed on another worker.
func (s *WebAgentService) RunOnce(ctx context.Context) (bool, error) {
	if !s.configured(ctx) {
		return false, nil
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, 5*time.Second)
	defer claimCancel()
	if err := s.repo.ExpireTasks(claimCtx); err != nil {
		return false, err
	}
	task, err := s.repo.ClaimTask(claimCtx, uuid.NewString(), s.leaseTTL)
	claimCancel()
	if err != nil || task == nil {
		return false, err
	}
	runCtx, cancel := context.WithDeadline(ctx, task.DeadlineAt)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		timer := time.NewTicker(s.heartbeat)
		defer timer.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-timer.C:
				if !s.chat.FeatureEnabled(runCtx) {
					cancel()
					return
				}
				probeCtx, probeCancel := context.WithTimeout(runCtx, s.heartbeat)
				err := s.repo.RenewTaskLease(probeCtx, task.ID, task.LeaseToken, s.leaseTTL)
				probeCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	var result json.RawMessage
	if task.GroupID == nil {
		err = ErrWebChatInvalidGroup
	} else if _, e := s.chat.GetSession(runCtx, task.UserID, task.SessionID); e != nil {
		err = e
	} else if _, _, e := s.chat.validateGroupModel(runCtx, task.UserID, *task.GroupID, task.Model); e != nil {
		err = e
	} else {
		result, err = s.execute(runCtx, task)
	}
	contextErr := runCtx.Err()
	cancel()
	<-heartbeatDone
	status, errorCode := WebAgentSucceeded, ""
	if err != nil {
		result = nil // Never publish an invalid or partially produced executor result.
		status = WebAgentFailed
		errorCode = "execution_failed"
		if errors.Is(contextErr, context.DeadlineExceeded) {
			errorCode = "execution_deadline"
		} else if errors.Is(contextErr, context.Canceled) {
			status = WebAgentInterrupted
			errorCode = "execution_interrupted"
		}
	}
	// Final state must survive browser/process cancellation and is lease-fenced.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	return true, s.repo.FinishTask(finishCtx, task.ID, task.LeaseToken, status, result, errorCode)
}
func (s *WebAgentService) execute(ctx context.Context, task *WebAgentTask) (result json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("executor panic")
		}
	}()
	var stepMu sync.Mutex
	var activeStep string
	var progressErr error
	result, err = s.executor.Execute(ctx, task, func(phase, name string) error {
		stepMu.Lock()
		defer stepMu.Unlock()
		if progressErr != nil {
			return progressErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if (phase != "started" && phase != "completed") || strings.TrimSpace(name) == "" || len(name) > 120 {
			progressErr = ErrWebAgentInvalid
			return progressErr
		}
		if (phase == "started" && activeStep != "") || (phase == "completed" && activeStep != name) {
			progressErr = ErrWebAgentInvalid
			return progressErr
		}
		data, _ := json.Marshal(map[string]string{"name": name})
		progressErr = s.repo.AppendTaskStep(ctx, task.ID, task.LeaseToken, "step."+phase, data)
		if progressErr == nil {
			if phase == "started" {
				activeStep = name
			} else {
				activeStep = ""
			}
		}
		return progressErr
	})
	stepMu.Lock()
	if err == nil {
		err = progressErr
		if activeStep != "" {
			err = ErrWebAgentInvalid
		}
	}
	stepMu.Unlock()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && (!json.Valid(result) || string(result) == "null" || len(result) > 512*1024) {
		err = ErrWebAgentInvalid
	}
	return result, err
}
