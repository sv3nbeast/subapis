package service

import "errors"

// Persist stable, non-sensitive causes in a task. Raw upstream bodies, prompts,
// renderer stderr and credential errors must not become user-visible messages.
type webAgentExecutionError struct {
	code  string
	cause error
}

func (e *webAgentExecutionError) Error() string { return e.cause.Error() }
func (e *webAgentExecutionError) Unwrap() error { return e.cause }
func webAgentFailure(code string, err error) error {
	if err == nil {
		return nil
	}
	var typed *webAgentExecutionError
	if errors.As(err, &typed) {
		return err
	}
	return &webAgentExecutionError{code: code, cause: err}
}
func webAgentFailureCode(err error) string {
	var typed *webAgentExecutionError
	if errors.As(err, &typed) {
		return typed.code
	}
	switch {
	case errors.Is(err, ErrWebChatFilesDisabled), errors.Is(err, ErrWebChatDocumentNotReady), errors.Is(err, ErrWebChatDocumentNotFound):
		return "document_unavailable"
	case errors.Is(err, ErrWebChatInvalidGroup), errors.Is(err, ErrWebChatInvalidModel):
		return "target_unavailable"
	case errors.Is(err, ErrWebAgentArtifactNotFound):
		return "source_unavailable"
	case errors.Is(err, ErrWebAgentStorageLimit):
		return "storage_limit"
	case errors.Is(err, ErrWebAgentStorageIdentity):
		return "storage_unavailable"
	case errors.Is(err, ErrWebAgentInvalid):
		return "invalid_task"
	default:
		return "execution_failed"
	}
}
