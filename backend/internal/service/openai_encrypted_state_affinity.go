package service

import "context"

// encryptedStateAffinityKey marks a request whose provider-owned encrypted
// reasoning/compaction state must stay on its sticky account. Without this
// marker the normal latency/load escape path can move a valid opaque blob to a
// different Grok credential before the provider gets a chance to decode it.
type encryptedStateAffinityKey struct{}

// WithOpenAIEncryptedStateAffinity enables strict sticky routing for one
// request. The marker is request-scoped and never changes global scheduling.
func WithOpenAIEncryptedStateAffinity(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, encryptedStateAffinityKey{}, true)
}

// OpenAIEncryptedStateAffinity reports whether strict sticky routing is active.
func OpenAIEncryptedStateAffinity(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(encryptedStateAffinityKey{}).(bool)
	return active
}
