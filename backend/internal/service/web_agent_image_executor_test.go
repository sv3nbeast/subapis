package service

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func imagePayload(t *testing.T, raw []byte, extra string) []byte {
	t.Helper()
	body := map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(raw)}}}
	if extra != "" {
		require.NoError(t, json.Unmarshal([]byte(extra), &body))
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return data
}

var agentPNGBytes = append([]byte("\x89PNG\r\n\x1a\n"), []byte("payload")...)

func TestWebAgentImageResponseTrustsBytesOverDeclaredFormat(t *testing.T) {
	out := &WebAgentImageOutput{}
	require.NoError(t, parseWebAgentImageResponse(imagePayload(t, agentPNGBytes, ""), out))
	require.Equal(t, "image/png", out.MIME)
	require.Equal(t, "png", out.Extension)
	require.Equal(t, agentPNGBytes, out.Data)

	jpeg := append([]byte{0xFF, 0xD8, 0xFF}, []byte("body")...)
	out = &WebAgentImageOutput{}
	require.NoError(t, parseWebAgentImageResponse(imagePayload(t, jpeg, ""), out))
	require.Equal(t, "image/jpeg", out.MIME, "a JPEG must not be stored as PNG just because the request asked for one")
	require.Equal(t, "jpg", out.Extension)
}

func TestWebAgentImageResponseRejectsUnusableReplies(t *testing.T) {
	for name, body := range map[string]string{
		"provider url instead of bytes": `{"data":[{"url":"https://example.invalid/i.png"}]}`,
		"no image data":                 `{"data":[]}`,
		"empty payload":                 `{}`,
		"not base64":                    `{"data":[{"b64_json":"!!!not-base64!!!"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			out := &WebAgentImageOutput{}
			require.Error(t, parseWebAgentImageResponse([]byte(body), out))
			require.Empty(t, out.Data)
		})
	}

	t.Run("unsupported format", func(t *testing.T) {
		out := &WebAgentImageOutput{}
		err := parseWebAgentImageResponse(imagePayload(t, []byte("GIF89a not supported"), ""), out)
		require.ErrorContains(t, err, "not a supported image format")
	})
}

func TestWebAgentImageResponseCarriesUsageForBilling(t *testing.T) {
	out := &WebAgentImageOutput{}
	body := imagePayload(t, agentPNGBytes, `{"usage":{"input_tokens":12,"output_tokens":34}}`)
	require.NoError(t, parseWebAgentImageResponse(body, out))
	require.NotNil(t, out.Generation.Usage)
	require.Equal(t, int64(12), out.Generation.Usage.InputTokens)
	require.Equal(t, int64(34), out.Generation.Usage.OutputTokens)
}

func TestWebAgentImageTitleStaysWithinArtifactBudget(t *testing.T) {
	require.Equal(t, webAgentImageFallbackTitle, webAgentImageTitle("   "))
	require.Equal(t, "一只 戴帽子的猫", webAgentImageTitle("  一只 \n 戴帽子的猫  "), "whitespace collapses into a single-line title")
	long := webAgentImageTitle(strings.Repeat("画", 400))
	require.Equal(t, 60, len([]rune(long)), "a long prompt is truncated to the artifact title budget")
	require.NoError(t, (&WebAgentArtifact{
		Kind: "image", Title: long, Filename: webAgentArtifactFilename(long, "png"), MIME: "image/png",
		BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff", SizeBytes: 10,
		SHA256: webAgentBlobDigest(agentPNGBytes),
	}).Validate())
}

// An image is its own preview: storing a second blob would leak an unreferenced
// file that storage accounting never reclaims.
func TestImageArtifactMustNotCarryAPreviewBlob(t *testing.T) {
	artifact := func() *WebAgentArtifact {
		return &WebAgentArtifact{
			Kind: "image", Title: "cat", Filename: webAgentArtifactFilename("cat", "png"), MIME: "image/png",
			BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff", SizeBytes: 10, SHA256: webAgentBlobDigest(agentPNGBytes),
		}
	}
	require.NoError(t, artifact().Validate())

	withPreview := artifact()
	withPreview.PreviewKey, withPreview.PreviewBytes = "ffffffff-ffff-ffff-ffff-fffffffffffe.pdf", 4
	require.ErrorIs(t, withPreview.Validate(), ErrWebAgentInvalid)

	// Office artifacts still require their rendered preview.
	office := &WebAgentArtifact{
		Kind: "document", Title: "doc", Filename: webAgentArtifactFilename("doc", "docx"),
		MIME: webAgentArtifactTypes["document"].mime, BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.docx",
		SizeBytes: 10, SHA256: webAgentBlobDigest(agentPNGBytes), Spec: json.RawMessage(`{"kind":"document","title":"doc"}`),
	}
	require.ErrorIs(t, office.Validate(), ErrWebAgentInvalid, "an Office artifact without a preview must be rejected")
	office.PreviewKey, office.PreviewBytes = "ffffffff-ffff-ffff-ffff-fffffffffffe.pdf", 4
	require.NoError(t, office.Validate())
}

// Providers disagree on format for the same request: gpt-image answers PNG
// while grok-imagine answers JPEG. The artifact must store what came back, so
// its key carries no extension and its MIME is the source of truth.
func TestImageArtifactStoresWhicheverFormatTheProviderReturned(t *testing.T) {
	artifact := func(mime, ext string) *WebAgentArtifact {
		return &WebAgentArtifact{
			Kind: "image", Title: "cat", Filename: webAgentArtifactFilename("cat", ext), MIME: mime,
			BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff", SizeBytes: 10, SHA256: webAgentBlobDigest(agentPNGBytes),
		}
	}
	for mime, ext := range webAgentImageFormats {
		require.NoError(t, artifact(mime, ext).Validate(), "a %s artifact must be storable", mime)
	}

	require.ErrorIs(t, artifact("image/gif", "gif").Validate(), ErrWebAgentInvalid, "an unsupported MIME must be refused")

	// A key committed with an extension cannot describe an image, because the
	// format is only known after that key is already reserved.
	withExtension := artifact("image/jpeg", "jpg")
	withExtension.BlobKey = "ffffffff-ffff-ffff-ffff-ffffffffffff.jpg"
	require.ErrorIs(t, withExtension.Validate(), ErrWebAgentInvalid)

	// The download filename follows the MIME, so a JPEG does not download as PNG.
	mismatched := artifact("image/jpeg", "png")
	require.ErrorIs(t, mismatched.Validate(), ErrWebAgentInvalid)
}

// Office artifacts keep their reserved extension: their renderer output format
// is known up front, and a mismatch would serve the wrong file type.
func TestOfficeArtifactStillRequiresItsReservedExtension(t *testing.T) {
	office := &WebAgentArtifact{
		Kind: "document", Title: "doc", Filename: webAgentArtifactFilename("doc", "docx"),
		MIME: webAgentArtifactTypes["document"].mime, BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.docx",
		PreviewKey: "ffffffff-ffff-ffff-ffff-fffffffffffe.pdf", PreviewBytes: 4,
		SizeBytes: 10, SHA256: webAgentBlobDigest(agentPNGBytes), Spec: json.RawMessage(`{"kind":"document","title":"doc"}`),
	}
	require.NoError(t, office.Validate())

	noExtension := *office
	noExtension.BlobKey = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	require.ErrorIs(t, noExtension.Validate(), ErrWebAgentInvalid, "an Office key must keep its extension")
}
