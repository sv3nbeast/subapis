package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateWebChatDocumentRejectsSpoofedTypes(t *testing.T) {
	_, _, ok := validateWebChatDocument("report.pdf", "application/pdf", []byte("not a pdf"))
	require.False(t, ok)
	_, _, ok = validateWebChatDocument("report.exe", "application/octet-stream", []byte("MZ"))
	require.False(t, ok)
}

func TestParseWebChatDocumentFixtures(t *testing.T) {
	docx := testDOCX(t, "DOCX quarterly revenue increased")
	pdfData := testPDF("PDF quarterly revenue increased")
	tests := []struct {
		name, ext string
		data      []byte
		contains  string
	}{
		{"txt", ".txt", []byte("TXT quarterly revenue increased"), "TXT quarterly"},
		{"markdown", ".md", []byte("# Notes\nMarkdown quarterly revenue increased"), "Markdown quarterly"},
		{"csv", ".csv", []byte("quarter,revenue\nQ1,120\n"), "Q1 | 120"},
		{"docx", ".docx", docx, "DOCX quarterly"},
		{"pdf", ".pdf", pdfData, "PDF quarterly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, chars, err := parseWebChatDocument(tt.ext, tt.data)
			require.NoError(t, err)
			require.Positive(t, chars)
			require.NotEmpty(t, chunks)
			var combined string
			for _, c := range chunks {
				combined += c.Content
			}
			require.Contains(t, combined, tt.contains)
			if tt.ext == ".pdf" {
				require.NotNil(t, chunks[0].PageNumber)
				require.Equal(t, 1, *chunks[0].PageNumber)
			}
			if tt.ext == ".csv" {
				require.Contains(t, chunks[1].LocationLabel, "2")
			}
		})
	}
}

func TestChunkSectionsBoundsAndLocations(t *testing.T) {
	page := 3
	chunks := chunkSections([]parsedSection{{page: &page, label: "第3页", text: string(bytes.Repeat([]byte("知识库内容。"), 400))}})
	require.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		require.LessOrEqual(t, len([]rune(c.Content)), 1400)
		require.Equal(t, &page, c.PageNumber)
	}
}

func testDOCX(t *testing.T, text string) []byte {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	w, err := z.Create("word/document.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`))
	require.NoError(t, err)
	require.NoError(t, z.Close())
	return out.Bytes()
}

func testPDF(text string) []byte {
	var b bytes.Buffer
	offsets := make([]int, 6)
	b.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 12 Tf 72 720 Td (%s) Tj ET\nendstream", len("BT /F1 12 Tf 72 720 Td () Tj ET\n")+len(text), text),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, obj := range objects {
		offsets[i+1] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}
