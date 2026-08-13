package service

import (
	"context"
	"errors"
	"fmt"
)

type anthropicStableCanaryOperation string

const (
	anthropicStableCanaryMessagesOperation anthropicStableCanaryOperation = "messages"
	anthropicStableCanaryRefreshOperation  anthropicStableCanaryOperation = "reactive_refresh"
)

var ErrAnthropicStableCanaryOutboundBlocked = errors.New("Anthropic stable canary outbound operation is blocked")

type anthropicStableCanaryMessageAuthKey struct{}
type anthropicStableCanaryRefreshAuthKey struct{}

func withAnthropicStableCanaryMessageAuthorization(ctx context.Context, accountID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, anthropicStableCanaryMessageAuthKey{}, accountID)
}

func withAnthropicStableCanaryRefreshAuthorization(ctx context.Context, accountID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, anthropicStableCanaryRefreshAuthKey{}, accountID)
}

func hasAnthropicStableCanaryMessageAuthorization(ctx context.Context, accountID int64) bool {
	value, _ := ctx.Value(anthropicStableCanaryMessageAuthKey{}).(int64)
	return ctx != nil && accountID > 0 && value == accountID
}

func hasAnthropicStableCanaryRefreshAuthorization(ctx context.Context, accountID int64) bool {
	value, _ := ctx.Value(anthropicStableCanaryRefreshAuthKey{}).(int64)
	return ctx != nil && accountID > 0 && value == accountID
}

func AnthropicStableCanaryRefreshAuthorized(ctx context.Context, accountID int64) bool {
	return hasAnthropicStableCanaryRefreshAuthorization(ctx, accountID)
}

func enforceAnthropicStableCanaryOutbound(ctx context.Context, account *Account, operation anthropicStableCanaryOperation) error {
	if account == nil || !account.HasAnthropicStableCanaryManagedFields() {
		return nil
	}
	switch operation {
	case anthropicStableCanaryMessagesOperation:
		if hasAnthropicStableCanaryMessageAuthorization(ctx, account.ID) {
			return nil
		}
	case anthropicStableCanaryRefreshOperation:
		if hasAnthropicStableCanaryRefreshAuthorization(ctx, account.ID) {
			return nil
		}
	}
	return fmt.Errorf("%w: operation=%s account_id=%d", ErrAnthropicStableCanaryOutboundBlocked, operation, account.ID)
}
