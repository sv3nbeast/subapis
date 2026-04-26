package service

import (
	"context"
	"errors"
	"strings"
)

// OpenAIClientCanceledError marks an OpenAI forward that was aborted because
// the inbound client request was already canceled, not because the account or
// upstream endpoint failed.
type OpenAIClientCanceledError struct {
	err error
}

func (e *OpenAIClientCanceledError) Error() string {
	if e == nil || e.err == nil {
		return "client request canceled"
	}
	return "client request canceled: " + e.err.Error()
}

func (e *OpenAIClientCanceledError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newOpenAIClientCanceledError(err error) error {
	return &OpenAIClientCanceledError{err: err}
}

// IsOpenAIClientCanceledError reports whether err is a client-aborted OpenAI
// forward. Handlers use this to avoid marking accounts unhealthy for caller
// timeouts/disconnects.
func IsOpenAIClientCanceledError(err error) bool {
	var target *OpenAIClientCanceledError
	return errors.As(err, &target)
}

func shouldTreatOpenAIRequestErrorAsClientCanceled(ctx context.Context, err error) bool {
	if err == nil || ctx == nil || ctx.Err() == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "context deadline exceeded") ||
		msg == "eof" ||
		strings.Contains(msg, "unexpected eof")
}
