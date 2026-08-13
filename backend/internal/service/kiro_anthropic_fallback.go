package service

import (
	"context"
	"time"
)

// KiroAnthropicFallbackPolicy is a request-scoped override sourced from the
// group row. It intentionally does not mutate process-wide Kiro resilience
// configuration, so one subscription group cannot affect another group.
type KiroAnthropicFallbackPolicy struct {
	Enabled              bool
	FirstSemanticTimeout time.Duration
	MaxAnthropicAttempts int
}

type kiroAnthropicFallbackPolicyContextKey struct{}

func KiroAnthropicFallbackPolicyForGroup(group *Group) KiroAnthropicFallbackPolicy {
	if group == nil || !group.EffectiveKiroAnthropicFallbackEnabled() {
		return KiroAnthropicFallbackPolicy{}
	}
	return KiroAnthropicFallbackPolicy{
		Enabled:              true,
		FirstSemanticTimeout: time.Duration(group.EffectiveKiroAnthropicFallbackFirstSemanticTimeoutSeconds()) * time.Second,
		MaxAnthropicAttempts: group.EffectiveKiroAnthropicFallbackMaxAnthropicAttempts(),
	}
}

func WithKiroAnthropicFallbackPolicy(ctx context.Context, policy KiroAnthropicFallbackPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, kiroAnthropicFallbackPolicyContextKey{}, policy)
}

func KiroAnthropicFallbackPolicyFromContext(ctx context.Context) KiroAnthropicFallbackPolicy {
	if ctx == nil {
		return KiroAnthropicFallbackPolicy{}
	}
	policy, _ := ctx.Value(kiroAnthropicFallbackPolicyContextKey{}).(KiroAnthropicFallbackPolicy)
	return policy
}
