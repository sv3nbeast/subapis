package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
	"github.com/redis/go-redis/v9"
)

const nianzsKiroCooldownTokenNamespace = "engine:nianzs:"

// dualKiroCooldownStore keeps the legacy and pinned nianzs state machines in
// separate Redis keyspaces. This prevents a canary request from changing the
// scheduling state observed by the rollback engine.
type dualKiroCooldownStore struct {
	legacy *kirocooldown.Store
	nianzs NianzsKiroCooldownStore
}

func newDualKiroCooldownStore(client *redis.Client) *dualKiroCooldownStore {
	return &dualKiroCooldownStore{
		legacy: kirocooldown.NewStore(client),
		nianzs: &nianzsKiroCooldownKeyspaceStore{inner: nianzscooldown.NewStore(client)},
	}
}

func (s *dualKiroCooldownStore) ReserveRequest(ctx context.Context, tokenKey string) (time.Duration, error) {
	return s.legacy.ReserveRequest(ctx, tokenKey)
}

func (s *dualKiroCooldownStore) MarkSuccess(ctx context.Context, tokenKey string) error {
	return s.legacy.MarkSuccess(ctx, tokenKey)
}

func (s *dualKiroCooldownStore) Mark429(ctx context.Context, tokenKey string) (time.Duration, error) {
	return s.legacy.Mark429(ctx, tokenKey)
}

func (s *dualKiroCooldownStore) MarkSuspended(ctx context.Context, tokenKey string) (time.Duration, error) {
	return s.legacy.MarkSuspended(ctx, tokenKey)
}

func (s *dualKiroCooldownStore) GetState(ctx context.Context, tokenKey string) (*kirocooldown.State, error) {
	return s.legacy.GetState(ctx, tokenKey)
}

func (s *dualKiroCooldownStore) ClearEarliestTransientCooldown(ctx context.Context, tokenKeys []string) (bool, error) {
	return s.legacy.ClearEarliestTransientCooldown(ctx, tokenKeys)
}

func (s *dualKiroCooldownStore) NianzsKiroCooldownStore() NianzsKiroCooldownStore {
	return s.nianzs
}

type nianzsKiroCooldownStoreProvider interface {
	NianzsKiroCooldownStore() NianzsKiroCooldownStore
}

type nianzsKiroCooldownKeyspaceStore struct {
	inner *nianzscooldown.Store
}

func nianzsKiroCooldownKey(tokenKey string) string {
	return nianzsKiroCooldownTokenNamespace + tokenKey
}

func (s *nianzsKiroCooldownKeyspaceStore) CheckCooldown(ctx context.Context, tokenKey string) error {
	return s.inner.CheckCooldown(ctx, nianzsKiroCooldownKey(tokenKey))
}

func (s *nianzsKiroCooldownKeyspaceStore) MarkSuccess(ctx context.Context, tokenKey string) error {
	return s.inner.MarkSuccess(ctx, nianzsKiroCooldownKey(tokenKey))
}

func (s *nianzsKiroCooldownKeyspaceStore) Mark429(ctx context.Context, tokenKey string) (time.Duration, error) {
	return s.inner.Mark429(ctx, nianzsKiroCooldownKey(tokenKey))
}

func (s *nianzsKiroCooldownKeyspaceStore) MarkSuspended(ctx context.Context, tokenKey string) (time.Duration, error) {
	return s.inner.MarkSuspended(ctx, nianzsKiroCooldownKey(tokenKey))
}

func (s *nianzsKiroCooldownKeyspaceStore) GetState(ctx context.Context, tokenKey string) (*nianzscooldown.State, error) {
	return s.inner.GetState(ctx, nianzsKiroCooldownKey(tokenKey))
}

func (s *nianzsKiroCooldownKeyspaceStore) ClearEarliestTransientCooldown(ctx context.Context, tokenKeys []string) (bool, error) {
	keys := make([]string, len(tokenKeys))
	for i, tokenKey := range tokenKeys {
		keys[i] = nianzsKiroCooldownKey(tokenKey)
	}
	return s.inner.ClearEarliestTransientCooldown(ctx, keys)
}
