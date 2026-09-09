package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type webAgentRuntime struct {
	*WebAgentOfficeExecutor
	probe     func(context.Context) error
	mu        sync.Mutex
	available atomic.Bool
	busy      bool
}

func (r *webAgentRuntime) Available() bool { return r.available.Load() }
func (r *webAgentRuntime) check(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// The renderer has one render slot. Do not mistake its legitimate busy
	// period for a health failure or revoke the currently executing task.
	if r.busy {
		return nil
	}
	err := r.probe(ctx)
	r.available.Store(err == nil)
	return err
}
func (r *webAgentRuntime) WaitReady(ctx context.Context) error {
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := r.check(probeCtx)
		cancel()
		if err == nil {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (r *webAgentRuntime) Execute(ctx context.Context, t *WebAgentTask, p func(string, string) error) (*WebAgentArtifact, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := r.check(probeCtx)
	cancel()
	if err != nil {
		return nil, ErrWebAgentUnavailable
	}
	r.mu.Lock()
	r.busy = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.busy = false; r.mu.Unlock() }()
	return r.WebAgentOfficeExecutor.Execute(ctx, t, p)
}
func (r *webAgentRuntime) Maintain(ctx context.Context) error {
	cleanupErr := r.WebAgentOfficeExecutor.Maintain(ctx)
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	probeErr := r.check(probeCtx)
	return errors.Join(cleanupErr, probeErr)
}

// Called once by dependency wiring before HTTP serving. WaitReady prevents it
// from claiming persisted jobs until both the local gateway and renderer respond.
func (s *WebChatService) ConfigureAgent(cfg config.WebAgentConfig, port int) error {
	if !cfg.Enabled {
		return nil
	}
	if s == nil || s.webChatKeyRepo == nil {
		return ErrWebAgentUnavailable
	}
	repo, ok := s.repo.(WebAgentRepository)
	if !ok {
		return ErrWebAgentUnavailable
	}
	artifacts, ok := s.repo.(WebAgentArtifactRepository)
	if !ok {
		return ErrWebAgentUnavailable
	}
	storage, ok := s.repo.(WebAgentStorageRepository)
	if !ok {
		return ErrWebAgentUnavailable
	}
	if strings.TrimSpace(cfg.StoragePath) == "" {
		return ErrWebAgentInvalid
	}
	renderer, err := NewWebAgentOfficeClient(cfg.RendererURL, cfg.RendererToken)
	if err != nil {
		return err
	}
	model, err := NewWebAgentModelClient(port)
	if err != nil {
		return err
	}
	store, err := NewWebAgentFileStore(cfg.StoragePath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = storage.RegisterArtifactStore(ctx, store.StorageID()); err != nil {
		return err
	}
	runtime := &webAgentRuntime{WebAgentOfficeExecutor: NewWebAgentOfficeExecutor(NewWebAgentModelPlanner(s, model), renderer, store, artifacts)}
	runtime.probe = func(ctx context.Context) error {
		if err := storage.RegisterArtifactStore(ctx, store.StorageID()); err != nil {
			return err
		}
		if err := probeAgentHealth(ctx, model.http, model.origin+"/health", ""); err != nil {
			return err
		}
		return probeAgentHealth(ctx, renderer.http, renderer.endpoint+"/health", renderer.token)
	}
	s.artifacts = NewWebAgentArtifactService(artifacts, s, store)
	s.agent = NewWebAgentService(repo, s, runtime)
	s.agent.Start()
	return nil
}
func (s *WebChatService) StopAgent() {
	if s != nil {
		s.agent.Stop()
	}
}
func probeAgentHealth(ctx context.Context, client *http.Client, url string, rendererToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if rendererToken != "" {
		req.Header.Set("Authorization", "Bearer "+rendererToken)
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrWebAgentUnavailable
	}
	if rendererToken == "" {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return ErrWebAgentUnavailable
	}
	var health struct {
		Status   string   `json:"status"`
		Protocol int      `json:"protocol_version"`
		Kinds    []string `json:"kinds"`
	}
	if json.Unmarshal(data, &health) != nil || health.Status != "ok" || health.Protocol != 1 {
		return ErrWebAgentUnavailable
	}
	kinds := map[string]bool{}
	for _, kind := range health.Kinds {
		kinds[kind] = true
	}
	if !kinds["slides"] || !kinds["document"] || !kinds["spreadsheet"] {
		return ErrWebAgentUnavailable
	}
	return nil
}
