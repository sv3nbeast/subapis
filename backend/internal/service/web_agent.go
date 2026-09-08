package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	WebAgentQueued          = "queued"
	WebAgentRunning         = "running"
	WebAgentCancelRequested = "cancel_requested"
	WebAgentSucceeded       = "succeeded"
	WebAgentFailed          = "failed"
	WebAgentCancelled       = "cancelled"
	WebAgentInterrupted     = "interrupted"
	WebAgentMaxSteps        = 16
	WebAgentMaxActiveTasks  = 3
	WebAgentTaskTimeout     = 10 * time.Minute
)

var (
	ErrWebAgentUnavailable = infraerrors.ServiceUnavailable("WEB_AGENT_UNAVAILABLE", "task execution is not configured")
	ErrWebAgentNotFound    = infraerrors.NotFound("WEB_AGENT_NOT_FOUND", "task not found")
	ErrWebAgentInvalid     = infraerrors.BadRequest("WEB_AGENT_INVALID", "invalid task request")
	ErrWebAgentConflict    = infraerrors.Conflict("WEB_AGENT_IDEMPOTENCY_CONFLICT", "idempotency key was used for a different request")
	ErrWebAgentBusy        = infraerrors.Conflict("WEB_AGENT_BUSY", "too many active tasks")
	ErrWebAgentLeaseLost   = infraerrors.Conflict("WEB_AGENT_LEASE_LOST", "task cancelled or execution lease lost")
)

type WebAgentTask struct {
	ID               int64           `json:"id"`
	UserID           int64           `json:"-"`
	SessionID        int64           `json:"session_id"`
	GroupID          *int64          `json:"group_id"`
	Model            string          `json:"model"`
	Kind             string          `json:"kind"`
	Prompt           string          `json:"prompt"`
	DocumentIDs      []int64         `json:"document_ids"`
	SourceArtifactID *int64          `json:"source_artifact_id,omitempty"`
	SessionSnapshot  json.RawMessage `json:"-"`
	IdempotencyKey   string          `json:"-"`
	RequestHash      string          `json:"-"`
	Status           string          `json:"status"`
	Result           json.RawMessage `json:"result,omitempty"`
	ErrorCode        string          `json:"error_code,omitempty"`
	StepCount        int             `json:"step_count"`
	LeaseToken       string          `json:"-"`
	DeadlineAt       time.Time       `json:"deadline_at"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
}
type WebAgentTaskEvent struct {
	ID        int64           `json:"id"`
	TaskID    int64           `json:"task_id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}
type WebAgentCreateRequest struct {
	SourceArtifactID *int64  `json:"source_artifact_id,omitempty"`
	Kind             string  `json:"kind"`
	Prompt           string  `json:"prompt"`
	DocumentIDs      []int64 `json:"document_ids"`
	IdempotencyKey   string  `json:"idempotency_key"`
}
type WebAgentRepository interface {
	CreateTask(context.Context, *WebAgentTask) (*WebAgentTask, error)
	GetTask(context.Context, int64, int64) (*WebAgentTask, error)
	ListTasks(context.Context, int64, int64, int64) ([]WebAgentTask, error)
	ListTaskEvents(context.Context, int64, int64, int64) ([]WebAgentTaskEvent, error)
	CancelTask(context.Context, int64, int64) (*WebAgentTask, error)
	ClaimTask(context.Context, string, time.Duration) (*WebAgentTask, error)
	RenewTaskLease(context.Context, int64, string, time.Duration) error
	AppendTaskStep(context.Context, int64, string, string, json.RawMessage) error
	FinishTask(context.Context, int64, string, string, json.RawMessage, string) error
	ExpireTasks(context.Context) error
}

func (t WebAgentTask) Terminal() bool {
	switch t.Status {
	case WebAgentSucceeded, WebAgentFailed, WebAgentCancelled, WebAgentInterrupted:
		return true
	}
	return false
}
func normalizeWebAgentRequest(req WebAgentCreateRequest) (WebAgentCreateRequest, error) {
	if req.SourceArtifactID != nil && *req.SourceArtifactID <= 0 {
		return req, ErrWebAgentInvalid
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Kind = strings.TrimSpace(req.Kind)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	switch req.Kind {
	case "slides", "spreadsheet", "document":
	default:
		return req, ErrWebAgentInvalid
	}
	if !utf8.ValidString(req.Prompt) || req.Prompt == "" || utf8.RuneCountInString(req.Prompt) > 20000 || len(req.DocumentIDs) > 20 {
		return req, ErrWebAgentInvalid
	}
	if len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 128 {
		return req, ErrWebAgentInvalid
	}
	for _, c := range req.IdempotencyKey {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return req, ErrWebAgentInvalid
		}
	}
	ids := append([]int64{}, req.DocumentIDs...)
	req.DocumentIDs = []int64{}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return req, ErrWebAgentInvalid
		}
		// Attachment order is semantic (e.g. "compare the first file to the
		// second"). Deduplicate without sorting or rewriting that order.
		if !seen[id] {
			req.DocumentIDs = append(req.DocumentIDs, id)
			seen[id] = true
		}
	}
	return req, nil
}
func webAgentRequestHash(sessionID int64, req WebAgentCreateRequest) string {
	data, _ := json.Marshal(struct {
		SessionID int64
		Request   WebAgentCreateRequest
	}{sessionID, req})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
