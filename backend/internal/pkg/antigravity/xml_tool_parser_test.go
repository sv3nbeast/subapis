package antigravity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDrainEmbeddedXMLToolText_ConvertsEscapedMCPXML(t *testing.T) {
	cleaned, calls, pending := drainEmbeddedXMLToolText(`Before &lt;mcp__workspace__read_file&gt;{"path":"/tmp/demo.txt"}&lt;/mcp__workspace__read_file&gt; After`)

	require.Empty(t, pending)
	require.Equal(t, "Before  After", cleaned)
	require.Len(t, calls, 1)
	require.Equal(t, "mcp__workspace__read_file", calls[0].name)
	require.Equal(t, "/tmp/demo.txt", calls[0].input["path"])
}

func TestDrainEmbeddedXMLToolText_ReturnsPendingIncompleteMCPXML(t *testing.T) {
	cleaned, calls, pending := drainEmbeddedXMLToolText(`Before <mcp__workspace__read_file>{"path":`)

	require.Equal(t, "Before ", cleaned)
	require.Empty(t, calls)
	require.Equal(t, `<mcp__workspace__read_file>{"path":`, pending)
}

func TestDrainEmbeddedXMLToolText_ReturnsPendingSplitEscapedMCPXMLStart(t *testing.T) {
	cleaned, calls, pending := drainEmbeddedXMLToolText(`Before &lt;mc`)

	require.Equal(t, "Before ", cleaned)
	require.Empty(t, calls)
	require.Equal(t, `&lt;mc`, pending)
}
