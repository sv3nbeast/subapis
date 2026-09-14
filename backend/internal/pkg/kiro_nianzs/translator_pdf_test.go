package kiro

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// buildPDFDocumentPart 构造与 Claude Code 一致的 document content block。
func buildPDFDocumentPart(t *testing.T, name string, raw []byte) gjson.Result {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"type": "document",
		"name": name,
		"source": map[string]any{
			"type":       "base64",
			"media_type": "application/pdf",
			"data":       base64.StdEncoding.EncodeToString(raw),
		},
	})
	if err != nil {
		t.Fatalf("marshal document part: %v", err)
	}
	return gjson.ParseBytes(encoded)
}

// newTextPDF 生成一份带真实文本内容流的最小 PDF。
func newTextPDF(t *testing.T, lines ...string) []byte {
	t.Helper()
	var stream strings.Builder
	stream.WriteString("BT /F1 12 Tf 20 700 Td 14 TL\n")
	for _, line := range lines {
		fmt.Fprintf(&stream, "(%s) Tj T*\n", line)
	}
	stream.WriteString("ET")
	content := stream.String()

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, body := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

// newBinaryStreamPDF 生成一份没有可读文本层、但含 zlib 压缩二进制流的 PDF。
// 这类文件正是旧实现产出乱码的来源：解压后的字节按 UTF-8 解码会落进各种字母区间。
func newBinaryStreamPDF(t *testing.T) []byte {
	t.Helper()
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte((i*7 + 13) % 251)
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zlib: %v", err)
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	out.WriteString("1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	fmt.Fprintf(&out, "2 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", compressed.Len())
	out.Write(compressed.Bytes())
	out.WriteString("\nendstream\nendobj\n")
	out.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return out.Bytes()
}

// PDF 含文本层时必须还原出正文，供 Kiro 上游阅读。
func TestBuildDocumentTextFallbackExtractsEmbeddedText(t *testing.T) {
	raw := newTextPDF(t, "Invoice Number 26342000002922099781", "Total Amount 880.00")
	got := buildDocumentTextFallback(buildPDFDocumentPart(t, "invoice.pdf", raw))

	if !strings.Contains(got, "[Extracted PDF text]") {
		t.Fatalf("expected extracted text section, got %q", got)
	}
	for _, want := range []string{"26342000002922099781", "880.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("extracted text must contain %q, got %q", want, got)
		}
	}
	if !strings.Contains(got, "invoice.pdf") {
		t.Errorf("expected document name in header, got %q", got)
	}
}

// 无文本层的 PDF 绝不能把解压出的二进制垃圾当正文塞给模型，
// 而要给出明确说明，让模型能据此提示用户。
func TestBuildDocumentTextFallbackRejectsBinaryGarbage(t *testing.T) {
	raw := newBinaryStreamPDF(t)
	got := buildDocumentTextFallback(buildPDFDocumentPart(t, "scan.pdf", raw))

	if got == "" {
		t.Fatal("attachment must not be dropped silently")
	}
	if strings.Contains(got, "[Extracted PDF text]") {
		t.Fatalf("binary garbage must not be surfaced as extracted text, got %q", got)
	}
	if !strings.Contains(got, "[PDF text extraction unavailable") {
		t.Fatalf("expected an explicit unavailable notice, got %q", got)
	}
	// 说明文本本身应当简短，不应夹带原始二进制。
	if len([]rune(got)) > 400 {
		t.Errorf("notice should stay short, got %d runes: %q", len([]rune(got)), got)
	}
}

// 畸形 PDF 不得让网关热路径 panic。
func TestBuildDocumentTextFallbackSurvivesMalformedPDF(t *testing.T) {
	raw := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0x00, 0xff, 0xfe, 0x01}, 512)...)
	got := buildDocumentTextFallback(buildPDFDocumentPart(t, "broken.pdf", raw))
	if strings.Contains(got, "[Extracted PDF text]") {
		t.Fatalf("malformed PDF must not yield extracted text, got %q", got)
	}
}

// looksLikeReadableText 必须拦住误解码的二进制，同时放行中英文正文。
func TestLooksLikeReadableTextRejectsMisdecodedBinary(t *testing.T) {
	readable := []string{
		"电子发票（普通发票）发票号码：26342000002922099781",
		"Invoice Number 26342000002922099781 Total 880.00",
		"合计价税合计（大写）捌佰捌拾圆整",
	}
	for _, text := range readable {
		if !looksLikeReadableText(text) {
			t.Errorf("readable text rejected: %q", text)
		}
	}

	garbage := []string{
		"쾮I3\x013Ϋ\nnjp=~J~,zDId\x15ݬS\x10$\x0fۉFBaw\x1buO{\x03+b!}/٦",
		"NQW<\nZPhXtuۻ }V\x16{\x19hU0e gk2\x1d{Ç5b\x12qpξ'\"6BUц\x19̎T",
	}
	for _, text := range garbage {
		if looksLikeReadableText(text) {
			t.Errorf("misdecoded binary accepted as readable: %q", text)
		}
	}
}
