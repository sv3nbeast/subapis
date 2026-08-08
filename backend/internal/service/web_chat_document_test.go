package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateWebChatDocumentRejectsSpoofedTypes(t *testing.T) {
	_, _, ok := validateWebChatDocument("report.pdf", "application/pdf", []byte("not a pdf"))
	require.False(t, ok)
	_, _, ok = validateWebChatDocument("report.exe", "application/octet-stream", []byte("MZ"))
	require.False(t, ok)
	_, _, ok = validateWebChatDocument("binary.txt", "text/plain", []byte{0xff, 0xfe, 0x00})
	require.False(t, ok)
	var fakeZip bytes.Buffer
	z := zip.NewWriter(&fakeZip)
	_, err := z.Create("random.bin")
	require.NoError(t, err)
	require.NoError(t, z.Close())
	_, _, ok = validateWebChatDocument("fake.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", fakeZip.Bytes())
	require.False(t, ok)
	_, _, ok = validateWebChatDocument("valid.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", testDOCX(t, "valid"))
	require.True(t, ok)
	_, _, ok = validateWebChatDocument("valid.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testXLSX(t))
	require.True(t, ok)
}

func TestUploadReusesExistingDocumentBeforeQuotaAndStorage(t *testing.T) {
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[SettingKeyWebChatFilesEnabled] = "true"
	existing := &WebChatDocument{ID: 42, Status: WebChatDocumentStatusReady, OriginalName: "report.xlsx"}
	repo := &webChatDocumentRepoTestDouble{existingDoc: existing}
	storeCalls := 0
	documentService := NewWebChatDocumentService(repo, settings, nil, func(context.Context, *WebChatDocumentS3Config) (WebChatDocumentStore, error) {
		storeCalls++
		return nil, errors.New("storage should not be opened for a duplicate")
	})
	sessionID := int64(9)

	got, err := documentService.Upload(context.Background(), 7, nil, &sessionID, "report.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testXLSX(t))

	require.NoError(t, err)
	require.Same(t, existing, got)
	require.Zero(t, storeCalls)
}

func TestUploadRetriesExistingFailedDocument(t *testing.T) {
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[SettingKeyWebChatFilesEnabled] = "true"
	existing := &WebChatDocument{ID: 42, Status: WebChatDocumentStatusFailed}
	retried := &WebChatDocument{ID: 42, Status: WebChatDocumentStatusUploaded}
	repo := &webChatDocumentRepoTestDouble{existingDoc: existing, retryDoc: retried}
	documentService := NewWebChatDocumentService(repo, settings, nil, nil)
	sessionID := int64(9)

	got, err := documentService.Upload(context.Background(), 7, nil, &sessionID, "report.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testXLSX(t))

	require.NoError(t, err)
	require.Same(t, retried, got)
}

func TestParseWebChatDocumentParagraphLocationsAndLimits(t *testing.T) {
	chunks, _, err := parseWebChatDocument(".md", []byte("first paragraph\n\nsecond paragraph"))
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, "第1段", chunks[0].LocationLabel)
	require.Equal(t, "第2段", chunks[1].LocationLabel)

	_, _, err = parseWebChatDocument(".txt", bytes.Repeat([]byte("界"), webChatExtractedMaxChars+1))
	require.ErrorIs(t, err, ErrWebChatDocumentUnsafe)
}

func TestBuildWebChatKnowledgeContextOnlySnapshotsInjectedSources(t *testing.T) {
	chunks := []WebChatDocumentChunk{
		{DocumentID: 1, DocumentName: "first.txt", LocationLabel: "第1段", Content: strings.Repeat("甲", 200)},
		{DocumentID: 2, DocumentName: "second.txt", LocationLabel: "第2段", Content: strings.Repeat("乙", 200)},
	}
	sources, knowledge := buildWebChatKnowledgeContext(chunks, 180)
	require.LessOrEqual(t, len([]rune(knowledge)), 180)
	require.Len(t, sources, 1)
	require.Equal(t, "first.txt", sources[0].DocumentName)
	require.Contains(t, knowledge, sources[0].Excerpt)
	require.NotContains(t, knowledge, "second.txt")
}

func TestPrepareKnowledgeExplicitAttachmentsIgnoreKnowledgeToggle(t *testing.T) {
	projectID := int64(99)
	repo := &webChatDocumentRepoTestDouble{
		searchChunks: []WebChatDocumentChunk{{
			ID: 7, DocumentID: 42, DocumentName: "report.xlsx", LocationLabel: "工作表1 · 第1行", Content: "quarter | revenue",
		}},
	}
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[SettingKeyWebChatFilesEnabled] = "true"
	documentService := NewWebChatDocumentService(repo, settings, nil, nil)

	sources, knowledge, err := documentService.PrepareKnowledge(context.Background(), 7, &WebChatSession{ProjectID: &projectID}, 100, 101, "这个文件有几列", []int64{42}, false)

	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Contains(t, knowledge, "report.xlsx")
	require.Contains(t, knowledge, "quarter | revenue")
	require.Contains(t, knowledge, "附件1")
	require.Equal(t, int64(0), repo.searchedProjectID)
}

func TestRunOnePersistsCompletionFailureAsRetryOrFailed(t *testing.T) {
	repo := &webChatDocumentRepoTestDouble{
		job:         &WebChatDocument{ID: 7, ObjectKey: "docs/7.txt", Extension: ".txt", Status: WebChatDocumentStatusUploaded, LeaseOwner: "owner", AttemptCount: 3},
		completeErr: errors.New("database write failed"),
	}
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[settingKeyWebChatDocumentS3] = mustWebChatJSON(t, WebChatDocumentS3Config{Bucket: "documents", AccessKeyID: "key", SecretAccessKey: "secret"})
	service := NewWebChatDocumentService(repo, settings, passthroughSecretEncryptor{}, func(context.Context, *WebChatDocumentS3Config) (WebChatDocumentStore, error) {
		return webChatDocumentStoreTestDouble{data: []byte("safe text")}, nil
	})
	err := service.runOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, repo.failCalls)
	require.Contains(t, repo.lastFailure, "database write failed")
}

func TestUpdateAdminConfigRequiresDedicatedReachableStorage(t *testing.T) {
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[settingKeyBackupS3Config] = mustWebChatJSON(t, BackupS3Config{Endpoint: "https://r2.example", Bucket: "shared"})
	headCalls := 0
	service := NewWebChatDocumentService(&webChatDocumentRepoTestDouble{}, settings, passthroughSecretEncryptor{}, func(context.Context, *WebChatDocumentS3Config) (WebChatDocumentStore, error) {
		headCalls++
		return webChatDocumentStoreTestDouble{}, nil
	})
	_, err := service.UpdateAdminConfig(context.Background(), WebChatDocumentAdminConfig{
		Enabled: true,
		Limits:  WebChatDocumentLimits{MaxFileBytes: 1, MaxFilesPerProject: 1, MaxBytesPerUser: 1},
		S3:      WebChatDocumentS3Config{Endpoint: "https://r2.example/", Bucket: "SHARED", AccessKeyID: "key", SecretAccessKey: "secret"},
	})
	require.ErrorIs(t, err, ErrWebChatStorageShared)
	require.Zero(t, headCalls)
	require.NotEqual(t, "true", settings.values[SettingKeyWebChatFilesEnabled])
}

func TestParseWebChatDocumentFixtures(t *testing.T) {
	docx := testDOCX(t, "DOCX quarterly revenue increased")
	xlsx := testXLSX(t)
	pdfData := testPDF("PDF quarterly revenue increased")
	tests := []struct {
		name, ext string
		data      []byte
		contains  string
	}{
		{"txt", ".txt", []byte("TXT quarterly revenue increased"), "TXT quarterly"},
		{"markdown", ".md", []byte("# Notes\nMarkdown quarterly revenue increased"), "Markdown quarterly"},
		{"csv", ".csv", []byte("quarter,revenue\nQ1,120\n"), "Q1 | 120"},
		{"xlsx", ".xlsx", xlsx, "Q1 | 120"},
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
	types, err := z.Create("[Content_Types].xml")
	require.NoError(t, err)
	_, err = types.Write([]byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))
	require.NoError(t, err)
	w, err := z.Create("word/document.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`))
	require.NoError(t, err)
	require.NoError(t, z.Close())
	return out.Bytes()
}

func testXLSX(t *testing.T) []byte {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	entries := map[string]string{
		"[Content_Types].xml":      `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"xl/workbook.xml":          `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/sharedStrings.xml":     `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2"><si><t>quarter</t></si><si><t>revenue</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>Q1</t></is></c><c r="B2"><v>120</v></c></row></sheetData></worksheet>`,
	}
	for name, content := range entries {
		file, err := z.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	return out.Bytes()
}

type passthroughSecretEncryptor struct{}

func (passthroughSecretEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (passthroughSecretEncryptor) Decrypt(value string) (string, error) { return value, nil }

type webChatDocumentSettingsTestDouble struct{ values map[string]string }

func newWebChatDocumentSettingsTestDouble() *webChatDocumentSettingsTestDouble {
	return &webChatDocumentSettingsTestDouble{values: map[string]string{}}
}
func (s *webChatDocumentSettingsTestDouble) Get(_ context.Context, key string) (*Setting, error) {
	return &Setting{Key: key, Value: s.values[key]}, nil
}
func (s *webChatDocumentSettingsTestDouble) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *webChatDocumentSettingsTestDouble) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}
func (s *webChatDocumentSettingsTestDouble) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = s.values[key]
	}
	return values, nil
}
func (s *webChatDocumentSettingsTestDouble) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}
func (s *webChatDocumentSettingsTestDouble) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}
func (s *webChatDocumentSettingsTestDouble) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

type webChatDocumentStoreTestDouble struct{ data []byte }

func (s webChatDocumentStoreTestDouble) Upload(context.Context, string, io.Reader, string) (int64, error) {
	return int64(len(s.data)), nil
}
func (s webChatDocumentStoreTestDouble) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (webChatDocumentStoreTestDouble) Delete(context.Context, string) error { return nil }
func (webChatDocumentStoreTestDouble) HeadBucket(context.Context) error     { return nil }

type webChatDocumentRepoTestDouble struct {
	job               *WebChatDocument
	existingDoc       *WebChatDocument
	retryDoc          *WebChatDocument
	createErr         error
	findErr           error
	completeErr       error
	failCalls         int
	lastFailure       string
	searchChunks      []WebChatDocumentChunk
	searchedProjectID int64
}

func (r *webChatDocumentRepoTestDouble) CreateDocument(context.Context, *WebChatDocument, WebChatDocumentLimits) error {
	if r.createErr != nil {
		return r.createErr
	}
	return nil
}
func (r *webChatDocumentRepoTestDouble) FindDocumentByScopeHash(context.Context, int64, *int64, *int64, string) (*WebChatDocument, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.existingDoc == nil {
		return nil, ErrWebChatDocumentNotFound
	}
	return r.existingDoc, nil
}
func (r *webChatDocumentRepoTestDouble) ListProjectDocuments(context.Context, int64, int64) ([]WebChatDocument, error) {
	return nil, nil
}
func (r *webChatDocumentRepoTestDouble) GetDocument(context.Context, int64, int64) (*WebChatDocument, error) {
	return nil, nil
}
func (r *webChatDocumentRepoTestDouble) SetDocumentEnabled(context.Context, int64, int64, bool) (*WebChatDocument, error) {
	return nil, nil
}
func (r *webChatDocumentRepoTestDouble) RetryDocument(context.Context, int64, int64) (*WebChatDocument, error) {
	if r.retryDoc != nil {
		return r.retryDoc, nil
	}
	return nil, nil
}
func (r *webChatDocumentRepoTestDouble) DocumentUsage(context.Context, int64, *int64) (int, int64, error) {
	return 0, 0, nil
}
func (r *webChatDocumentRepoTestDouble) MarkDocumentDeleting(context.Context, int64, int64) error {
	return nil
}
func (r *webChatDocumentRepoTestDouble) ClaimDocumentJob(context.Context, string, time.Duration) (*WebChatDocument, error) {
	return r.job, nil
}
func (r *webChatDocumentRepoTestDouble) CompleteDocument(context.Context, int64, string, []WebChatDocumentChunk, int64) error {
	return r.completeErr
}
func (r *webChatDocumentRepoTestDouble) FailDocument(_ context.Context, _ int64, _ string, message string, _ time.Time) error {
	r.failCalls++
	r.lastFailure = message
	return nil
}
func (r *webChatDocumentRepoTestDouble) FinishDocumentDelete(context.Context, int64, string) error {
	return nil
}
func (r *webChatDocumentRepoTestDouble) SearchDocumentChunks(_ context.Context, _ int64, projectID int64, _ []int64, _ string, _ int) ([]WebChatDocumentChunk, error) {
	r.searchedProjectID = projectID
	return r.searchChunks, nil
}
func (r *webChatDocumentRepoTestDouble) LinkMessageDocuments(context.Context, int64, int64, []int64) error {
	return nil
}
func (r *webChatDocumentRepoTestDouble) MessageDocumentIDs(context.Context, int64, int64) ([]int64, error) {
	return nil, nil
}
func (r *webChatDocumentRepoTestDouble) UpdateMessageSources(context.Context, int64, int64, []WebChatSource) error {
	return nil
}
func (r *webChatDocumentRepoTestDouble) MarkProjectDocumentsDeleting(context.Context, int64, int64) error {
	return nil
}
func (r *webChatDocumentRepoTestDouble) MarkSessionDocumentsDeleting(context.Context, int64, int64) error {
	return nil
}

func mustWebChatJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
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
