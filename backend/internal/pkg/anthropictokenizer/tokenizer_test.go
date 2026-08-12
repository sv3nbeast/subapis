//go:build unit

package anthropictokenizer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCountTokensMatchesAnthropicReferenceExamples(t *testing.T) {
	require.Equal(t, 3, CountTokens("hello world!"))
	require.Equal(t, 1, CountTokens("™"))
	require.Equal(t, 1, CountTokens("ϰ"))
	require.Equal(t, 1, CountTokens("<EOT>"))
}

func TestEstimateModernClaudeTextTokensMatchesCapturedLiteralBaselines(t *testing.T) {
	require.Equal(t, 1, EstimateModernClaudeTextTokens("2"))
	require.Equal(t, 4, EstimateModernClaudeTextTokens("2025-01"))
	require.Equal(t, 7, EstimateModernClaudeTextTokens("ELZTURDB"))
	require.Equal(t, 6, EstimateModernClaudeTextTokens("TZVNE5"))
}
