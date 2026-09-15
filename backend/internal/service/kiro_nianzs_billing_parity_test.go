package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/stretchr/testify/require"
)

// nianzsKiroFrameForBillingParity encodes one Kiro eventstream frame.
func nianzsKiroFrameForBillingParity(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	headers := bytes.NewBuffer(nil)
	require.NoError(t, headers.WriteByte(byte(len(":event-type"))))
	_, _ = headers.WriteString(":event-type")
	require.NoError(t, headers.WriteByte(7))
	require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(eventType))))
	_, _ = headers.WriteString(eventType)

	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(12+headers.Len()+len(payloadBytes)+4)))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}

// The Kiro translator reports one figure to the client and bills another, so
// the stream has to stay self-describing: whatever the billing parser recovers
// from the wire must equal the translator's own billing usage. Previously the
// terminal frame was rescaled to Kiro's context occupancy and the real numbers
// rode along in _sub2api_billing_usage; now the client-visible figure is the
// billing figure for ordinary models, and the marker alone carries it. Both
// shapes must reconcile, otherwise a usage change silently becomes a price
// change.
func TestNianzsKiroStreamWireReconcilesWithBillingUsage(t *testing.T) {
	cases := []struct {
		name  string
		model string
		ctx   nianzskiro.KiroRequestContext
	}{
		{
			name:  "ordinary model reports request-side input",
			model: "claude-opus-5",
			ctx:   nianzskiro.KiroRequestContext{ContextWindowTokens: 1_000_000},
		},
		{
			name:  "leaked 1m model suffix keeps provider occupancy",
			model: "claude-opus-5[1m]",
			ctx:   nianzskiro.KiroRequestContext{ContextWindowTokens: 1_000_000},
		},
		{
			// The signal that actually arrives in production: Claude Code strips
			// "[1m]" and sends the beta token instead.
			name:  "declared 1m client keeps provider occupancy",
			model: "claude-opus-5",
			ctx: nianzskiro.KiroRequestContext{
				ContextWindowTokens:           1_000_000,
				ClientDeclaredExtendedContext: true,
			},
		},
		{
			name:  "emulated cache buckets",
			model: "claude-opus-5",
			ctx: nianzskiro.KiroRequestContext{
				ContextWindowTokens: 1_000_000,
				CacheEmulationUsage: &nianzskiro.Usage{
					InputTokens:                25,
					CacheReadInputTokens:       4_757,
					CacheCreation5mInputTokens: 0,
					CacheCreation1hInputTokens: 0,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := bytes.NewBuffer(nil)
			stream.Write(nianzsKiroFrameForBillingParity(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "done"},
			}))
			stream.Write(nianzsKiroFrameForBillingParity(t, "contextUsageEvent", map[string]any{
				"contextUsageEvent": map[string]any{"contextUsagePercentage": 1.5487},
			}))
			stream.Write(nianzsKiroFrameForBillingParity(t, "messageMetadataEvent", map[string]any{
				"messageMetadataEvent": map[string]any{
					"tokenUsage": map[string]any{"outputTokens": 3, "totalTokens": 3},
				},
			}))

			var wire bytes.Buffer
			result, err := nianzskiro.StreamEventStreamAsAnthropicWithContext(
				context.Background(), stream, &wire, tc.model, 4_760, tc.ctx)
			require.NoError(t, err)

			var recovered ClaudeUsage
			for _, line := range strings.Split(wire.String(), "\n") {
				data, ok := extractAnthropicSSEDataLine(strings.TrimSpace(line))
				if !ok {
					continue
				}
				parseSSEUsagePassthrough(data, &recovered)
			}

			require.Equal(t, result.Usage.InputTokens, recovered.InputTokens,
				"billing input must survive the wire")
			require.Equal(t, result.Usage.OutputTokens, recovered.OutputTokens)
			require.Equal(t, result.Usage.CacheReadInputTokens, recovered.CacheReadInputTokens)
			require.Equal(t, result.Usage.CacheCreationInputTokens, recovered.CacheCreationInputTokens)
		})
	}
}
