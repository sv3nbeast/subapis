package service

import (
	"context"
	"time"
)

// GeminiTokenCache stores short-lived access tokens and coordinates refresh to avoid stampedes.
type GeminiTokenCache interface {
	// cacheKey should be stable for the token scope; for GeminiCli OAuth we primarily use project_id.
	GetAccessToken(ctx context.Context, cacheKey string) (string, error)
	SetAccessToken(ctx context.Context, cacheKey string, token string, ttl time.Duration) error
	DeleteAccessToken(ctx context.Context, cacheKey string) error

	AcquireRefreshLock(ctx context.Context, cacheKey string, ttl time.Duration) (bool, error)
	ReleaseRefreshLock(ctx context.Context, cacheKey string) error
}

// OAuthRefreshLeaseCache adds owner-aware refresh leases. OAuthRefreshAPI
// prefers this contract so an expired holder cannot delete a successor's lock.
// GeminiTokenCache remains unchanged for compatibility with existing caches.
type OAuthRefreshLeaseCache interface {
	AcquireRefreshLease(ctx context.Context, cacheKey, owner string, ttl time.Duration) (bool, error)
	ReleaseRefreshLease(ctx context.Context, cacheKey, owner string) error
}
