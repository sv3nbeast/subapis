package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type WebAgentPlan struct {
	Spec       json.RawMessage
	Generation *WebAgentGeneration
}
type WebAgentPlanner interface {
	Plan(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentPlan, error)
}
type WebAgentRenderer interface {
	Render(context.Context, string, json.RawMessage) (*WebAgentRenderedArtifact, error)
}

// The only file tool is the private structured renderer. Neither prompts nor
// planner output can choose an executable, network endpoint or storage path.
type WebAgentOfficeExecutor struct {
	planner  WebAgentPlanner
	renderer WebAgentRenderer
	store    WebAgentBlobStore
	repo     WebAgentArtifactRepository
}

func NewWebAgentOfficeExecutor(planner WebAgentPlanner, renderer WebAgentRenderer, store WebAgentBlobStore, repo WebAgentArtifactRepository) *WebAgentOfficeExecutor {
	return &WebAgentOfficeExecutor{planner: planner, renderer: renderer, store: store, repo: repo}
}
func (e *WebAgentOfficeExecutor) Execute(ctx context.Context, task *WebAgentTask, progress func(string, string) error) (artifact *WebAgentArtifact, err error) {
	if e == nil || e.planner == nil || e.renderer == nil || e.store == nil || e.repo == nil {
		return nil, ErrWebAgentUnavailable
	}
	if task == nil || progress == nil {
		return nil, ErrWebAgentInvalid
	}
	if _, ok := webAgentArtifactTypes[task.Kind]; !ok {
		return nil, ErrWebAgentInvalid
	}
	artifact = &WebAgentArtifact{Kind: task.Kind}
	ready := false
	defer func() {
		// Also runs on a panic before the result reaches the task worker.
		if !ready {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = e.Discard(cleanup, artifact)
		}
	}()
	var source *WebAgentArtifact
	if task.SourceArtifactID != nil {
		source, err = e.repo.GetArtifact(ctx, task.UserID, *task.SourceArtifactID)
		if err != nil {
			return artifact, err
		}
		if source.UserID != task.UserID || source.SessionID != task.SessionID || source.Kind != task.Kind {
			return artifact, ErrWebAgentArtifactNotFound
		}
	}
	if err = progress("started", "生成内容"); err != nil {
		return artifact, err
	}
	plan, err := e.planner.Plan(ctx, task, source)
	if plan != nil {
		artifact.Generation = plan.Generation
	}
	if err != nil {
		return artifact, err
	}
	if plan == nil || len(plan.Spec) > 1<<20 || !json.Valid(plan.Spec) {
		return artifact, ErrWebAgentInvalid
	}
	var header struct{ Kind, Title string }
	if json.Unmarshal(plan.Spec, &header) != nil || header.Kind != task.Kind {
		return artifact, ErrWebAgentInvalid
	}
	artifact.Title, artifact.Spec = header.Title, plan.Spec
	if err = progress("completed", "生成内容"); err != nil {
		return artifact, err
	}
	if err = progress("started", "生成文件和预览"); err != nil {
		return artifact, err
	}
	rendered, err := e.renderer.Render(ctx, task.Kind, plan.Spec)
	if err != nil {
		return artifact, webAgentFailure("render_failed", err)
	}
	if rendered == nil {
		return artifact, ErrWebAgentInvalid
	}
	artifact.MIME = rendered.MIME
	artifact.Filename = webAgentArtifactFilename(header.Title, rendered.Extension)
	artifact.SHA256 = rendered.SHA256
	artifact.SizeBytes, artifact.PreviewBytes = int64(len(rendered.File)), int64(len(rendered.PreviewPDF))
	if err = progress("completed", "生成文件和预览"); err != nil {
		return artifact, err
	}
	if err = progress("started", "保存成果"); err != nil {
		return artifact, err
	}
	if artifact.BlobKey, err = e.store.Put(ctx, rendered.Extension, rendered.File); err != nil {
		return artifact, webAgentFailure("storage_failed", err)
	}
	if artifact.PreviewKey, err = e.store.Put(ctx, "pdf", rendered.PreviewPDF); err != nil {
		return artifact, webAgentFailure("storage_failed", err)
	}
	if err = artifact.Validate(); err != nil {
		return artifact, err
	}
	if err = progress("completed", "保存成果"); err != nil {
		return artifact, err
	}
	ready = true
	return artifact, nil
}
func (e *WebAgentOfficeExecutor) Discard(ctx context.Context, a *WebAgentArtifact) error {
	if a == nil {
		return nil
	}
	var errs []error
	for _, key := range []string{a.BlobKey, a.PreviewKey} {
		if key != "" {
			errs = append(errs, e.store.Remove(ctx, key))
		}
	}
	return errors.Join(errs...)
}
