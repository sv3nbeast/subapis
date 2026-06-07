package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/stretchr/testify/require"
)

type kiroStreamFailoverCooldownStore struct {
	mark429Calls int
}

func (s *kiroStreamFailoverCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroStreamFailoverCooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *kiroStreamFailoverCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	s.mark429Calls++
	return time.Minute, nil
}

func (s *kiroStreamFailoverCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroStreamFailoverCooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	return nil, nil
}

func (s *kiroStreamFailoverCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

func TestGatewayServiceKiroStreamExceptionMarks429Cooldown(t *testing.T) {
	store := &kiroStreamFailoverCooldownStore{}
	svc := &GatewayService{kiroCooldownStore: store}
	account := &Account{
		ID:       1459,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}
	err := &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
		Cause: &kiropkg.UpstreamExceptionError{
			ExceptionType: "ThrottlingException",
			Message:       "Too many requests, please wait before trying again.",
		},
	}

	failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, err)

	require.NotNil(t, failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, 1, store.mark429Calls)
}

func TestGatewayServiceKiroEmptyStreamIsRetryableFailover(t *testing.T) {
	svc := &GatewayService{kiroCooldownStore: &kiroStreamFailoverCooldownStore{}}
	account := &Account{ID: 1459, Platform: PlatformKiro, Type: AccountTypeOAuth}
	err := errors.New("stream read error: empty kiro event stream: no assistant output")

	failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, err)

	require.NotNil(t, failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
}
