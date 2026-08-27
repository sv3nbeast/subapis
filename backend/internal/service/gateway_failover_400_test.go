//go:build unit

package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldFailoverOn400AllowsOnlyExplicitBetaCompatibilityErrors(t *testing.T) {
	svc := &GatewayService{}
	for _, message := range []string{
		"anthropic-beta header is required",
		"request requires beta header",
		"beta feature is not enabled for this account",
	} {
		t.Run(message, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"message":%q}}`, message))
			require.True(t, svc.shouldFailoverOn400(body))
		})
	}
}

func TestShouldFailoverOn400RejectsDeterministicHistoryAndToolErrors(t *testing.T) {
	svc := &GatewayService{}
	for _, message := range []string{
		"Invalid signature in thinking block",
		"thinking or redacted_thinking blocks in the latest assistant message cannot be modified",
		"missing thought_signature",
		"tool_use block requires a matching tool_result",
		"tools must have unique names",
		"the model is thinking about a beta feature",
	} {
		t.Run(message, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"message":%q}}`, message))
			require.False(t, svc.shouldFailoverOn400(body))
		})
	}
}

func TestShouldFailoverOn400RejectsUnknownOrEmptyError(t *testing.T) {
	svc := &GatewayService{}
	require.False(t, svc.shouldFailoverOn400([]byte(`{"error":{"message":"bad request"}}`)))
	require.False(t, svc.shouldFailoverOn400(nil))
}
