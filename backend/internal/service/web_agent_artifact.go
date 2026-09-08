package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const webAgentArtifactMaxBytes = 32 << 20

type WebAgentRenderedArtifact struct {
	Extension  string
	MIME       string
	File       []byte
	PreviewPDF []byte
	SHA256     string
}

var webAgentArtifactTypes = map[string]struct{ ext, mime, entry string }{
	"slides":      {"pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", "ppt/presentation.xml"},
	"document":    {"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "word/document.xml"},
	"spreadsheet": {"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xl/workbook.xml"},
}

type WebAgentOfficeClient struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewWebAgentOfficeClient(endpoint, token string) (*WebAgentOfficeClient, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || len(token) < 32 {
		return nil, ErrWebAgentInvalid
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		// Plain HTTP is restricted to loopback or a single-label private service
		// name, configured by the operator. No request can choose a renderer URL.
		if u.Scheme != "http" || !(u.Hostname() == "localhost" || ip != nil && ip.IsLoopback() || ip == nil && !strings.Contains(u.Hostname(), ".")) {
			return nil, ErrWebAgentInvalid
		}
	}
	client := &http.Client{
		Timeout: 190 * time.Second,
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 185 * time.Second, TLSHandshakeTimeout: 5 * time.Second, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &WebAgentOfficeClient{endpoint: u.String(), token: token, http: client}, nil
}
func (c *WebAgentOfficeClient) Render(ctx context.Context, kind string, spec json.RawMessage) (*WebAgentRenderedArtifact, error) {
	if c == nil {
		return nil, ErrWebAgentUnavailable
	}
	expected, ok := webAgentArtifactTypes[kind]
	if !ok || len(spec) > 1<<20 || !json.Valid(spec) {
		return nil, ErrWebAgentInvalid
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(spec, &header) != nil || header.Kind != kind {
		return nil, ErrWebAgentInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/render", bytes.NewReader(spec))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("artifact renderer request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 422 {
		return nil, ErrWebAgentInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact renderer returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 48<<20+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 48<<20 {
		return nil, errors.New("artifact renderer response too large")
	}
	var result struct {
		Protocol  int    `json:"protocol_version"`
		Extension string `json:"extension"`
		MIME      string `json:"mime"`
		File      []byte `json:"file_base64"`
		Preview   []byte `json:"preview_pdf_base64"`
		SHA256    string `json:"file_sha256"`
		Size      int64  `json:"size_bytes"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("invalid artifact renderer response")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(result.File))
	if result.Protocol != 1 || result.Extension != expected.ext || result.MIME != expected.mime || int64(len(result.File)) != result.Size || result.SHA256 != hash ||
		len(result.File)+len(result.Preview) > webAgentArtifactMaxBytes || !bytes.HasPrefix(result.Preview, []byte("%PDF-")) {
		return nil, errors.New("artifact renderer integrity check failed")
	}
	if err = validateWebAgentOfficeArchive(result.File, expected.entry); err != nil {
		return nil, err
	}
	return &WebAgentRenderedArtifact{Extension: result.Extension, MIME: result.MIME, File: result.File, PreviewPDF: result.Preview, SHA256: hash}, nil
}
func validateWebAgentOfficeArchive(data []byte, required string) error {
	remaining := uint64(64 << 20)
	return validateWebAgentOfficeParts(data, required, 0, &remaining)
}

func validateWebAgentOfficeParts(data []byte, required string, depth int, remaining *uint64) error {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("invalid Office archive")
	}
	if len(z.File) > 2000 {
		return errors.New("Office archive has too many entries")
	}
	seen := make(map[string]bool)
	for _, f := range z.File {
		lower := strings.ToLower(f.Name)
		if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "..") || strings.Contains(f.Name, "\\") || seen[f.Name] || strings.Contains(lower, "vbaproject") || strings.Contains(lower, "macrosheets") || strings.HasSuffix(lower, ".bin") {
			return errors.New("unsafe Office archive entry")
		}
		seen[f.Name] = true
		if f.UncompressedSize64 > 16<<20 || f.UncompressedSize64 > *remaining {
			return errors.New("Office archive is too large")
		}
		*remaining -= f.UncompressedSize64
		reader, e := f.Open()
		if e != nil {
			return errors.New("invalid Office archive part")
		}
		// Read to EOF to verify ZIP CRCs, not only its central-directory metadata.
		part, readErr := io.ReadAll(io.LimitReader(reader, 16<<20+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || uint64(len(part)) != f.UncompressedSize64 {
			return errors.New("Office archive part integrity check failed")
		}
		// Native PowerPoint charts embed an editable workbook. Validate its
		// contents too, sharing the expansion budget and prohibiting recursion.
		if strings.HasSuffix(lower, ".xlsx") {
			if depth != 0 || required != "ppt/presentation.xml" || !strings.HasPrefix(lower, "ppt/embeddings/") {
				return errors.New("unexpected embedded Office workbook")
			}
			if e = validateWebAgentOfficeParts(part, "xl/workbook.xml", depth+1, remaining); e != nil {
				return e
			}
		}
		// Prevent active external relationships in the exported document.
		if strings.HasSuffix(f.Name, ".rels") {
			var relationships struct {
				Items []struct {
					Mode   string `xml:"TargetMode,attr"`
					Target string `xml:"Target,attr"`
				} `xml:"Relationship"`
			}
			e = xml.Unmarshal(part, &relationships)
			if e != nil {
				return errors.New("invalid Office relationships")
			}
			for _, r := range relationships.Items {
				if strings.EqualFold(r.Mode, "External") || strings.Contains(r.Target, ":") || strings.HasPrefix(r.Target, "//") || strings.Contains(r.Target, "\\") {
					return errors.New("external Office relationships are not allowed")
				}
			}
		}
	}
	if !seen[required] || !seen["[Content_Types].xml"] {
		return errors.New("Office archive is missing required parts")
	}
	return nil
}

type WebAgentBlobStore interface {
	Put(context.Context, string, []byte) (string, error)
	Open(context.Context, string) (io.ReadCloser, int64, error)
	Remove(context.Context, string) error
}
type WebAgentFileStore struct{ root string }

var webAgentBlobKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(pptx|xlsx|docx|pdf)$`)

func NewWebAgentFileStore(root string) (*WebAgentFileStore, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == "/" || root == "/tmp" || root == "/private/tmp" {
		return nil, ErrWebAgentInvalid
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrWebAgentInvalid
	}
	return &WebAgentFileStore{root: root}, nil
}
func (s *WebAgentFileStore) Put(ctx context.Context, extension string, data []byte) (key string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(data) == 0 || len(data) > webAgentArtifactMaxBytes {
		return "", ErrWebAgentInvalid
	}
	key = uuid.NewString() + "." + extension
	if !webAgentBlobKey.MatchString(key) {
		return "", ErrWebAgentInvalid
	}
	path := filepath.Join(s.root, key)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return key, nil
}
func (s *WebAgentFileStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	if !webAgentBlobKey.MatchString(key) {
		return nil, 0, ErrWebAgentInvalid
	}
	path := filepath.Join(s.root, key)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !before.Mode().IsRegular() || before.Size() > webAgentArtifactMaxBytes {
		return nil, 0, ErrWebAgentInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, ErrWebAgentInvalid
	}
	return f, info.Size(), nil
}
func (s *WebAgentFileStore) Remove(ctx context.Context, key string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !webAgentBlobKey.MatchString(key) {
		return ErrWebAgentInvalid
	}
	err := os.Remove(filepath.Join(s.root, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
