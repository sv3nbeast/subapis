package service

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const (
	antigravityExternalWorkerBinEnv           = "ANTIGRAVITY_EXTERNAL_WORKER_BIN"
	antigravityExternalWorkerBoringBinEnv     = "ANTIGRAVITY_EXTERNAL_WORKER_BIN_BORINGCRYPTO"
	antigravityExternalWorkerPreferBoringEnv  = "ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO"
	antigravityExternalWorkerStartTimeout     = 5 * time.Second
	antigravityExternalWorkerLoopbackTarget   = "127.0.0.1:0"
	antigravityExternalWorkerBinaryName       = "antigravityworker"
	antigravityExternalWorkerBoringBinaryName = "antigravityworker-boringcrypto"
)

type antigravityWorkerAnnouncement struct {
	Addr string `json:"addr"`
}

type antigravityExternalExecuteRequest struct {
	Method                       string                  `json:"method"`
	URL                          string                  `json:"url"`
	Headers                      http.Header             `json:"headers,omitempty"`
	BodyBase64                   string                  `json:"body_base64,omitempty"`
	ProxyURL                     string                  `json:"proxy_url,omitempty"`
	AccountConcurrency           int                     `json:"account_concurrency,omitempty"`
	ResponseHeaderTimeoutSeconds int                     `json:"response_header_timeout_seconds,omitempty"`
	TLSProfile                   *tlsfingerprint.Profile `json:"tls_profile,omitempty"`
}

type antigravityExternalWorkerExecutor struct {
	mu sync.Mutex

	cmd    *exec.Cmd
	addr   string
	client *http.Client
	bin    string
}

func antigravityExternalWorkerBinaryPath() string {
	candidates := antigravityExternalWorkerBinaryCandidates()
	for _, candidate := range candidates {
		if antigravityExternalWorkerBinaryExists(candidate) {
			return candidate
		}
	}
	return ""
}

func antigravityExternalWorkerEnabled() bool {
	return antigravityExternalWorkerBinaryPath() != ""
}

func antigravityExternalWorkerBinaryCandidates() []string {
	preferBoring := true
	if raw := strings.TrimSpace(os.Getenv(antigravityExternalWorkerPreferBoringEnv)); raw != "" {
		lower := strings.ToLower(raw)
		preferBoring = lower != "0" && lower != "false" && lower != "no"
	}

	var candidates []string
	appendIf := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			candidates = append(candidates, value)
		}
	}

	explicitBoring := strings.TrimSpace(os.Getenv(antigravityExternalWorkerBoringBinEnv))
	explicitDefault := strings.TrimSpace(os.Getenv(antigravityExternalWorkerBinEnv))
	autoBoring := antigravityExternalWorkerAutoPaths(antigravityExternalWorkerBoringBinaryName)
	autoDefault := antigravityExternalWorkerAutoPaths(antigravityExternalWorkerBinaryName)

	if preferBoring {
		appendIf(explicitBoring)
		for _, path := range autoBoring {
			appendIf(path)
		}
		appendIf(explicitDefault)
		for _, path := range autoDefault {
			appendIf(path)
		}
	} else {
		appendIf(explicitDefault)
		for _, path := range autoDefault {
			appendIf(path)
		}
		appendIf(explicitBoring)
		for _, path := range autoBoring {
			appendIf(path)
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	deduped := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		deduped = append(deduped, path)
	}
	return deduped
}

func antigravityExternalWorkerAutoPaths(binaryName string) []string {
	var paths []string
	appendIf := func(path string) {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}

	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		appendIf(filepath.Join(dataDir, "bin", binaryName))
	}
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		appendIf(filepath.Join("/app/data", "bin", binaryName))
	}
	if exePath, err := os.Executable(); err == nil && strings.TrimSpace(exePath) != "" {
		appendIf(filepath.Join(filepath.Dir(exePath), binaryName))
	}
	appendIf(filepath.Join("bin", binaryName))
	return paths
}

func antigravityExternalWorkerBinaryExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (e *antigravityExternalWorkerExecutor) do(req *http.Request, proxyURL string, account *Account, profile *tlsfingerprint.Profile, responseHeaderTimeout time.Duration) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	if err := e.ensureStarted(); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body for external worker: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	timeoutSeconds := 0
	if responseHeaderTimeout > 0 {
		timeoutSeconds = int(responseHeaderTimeout.Seconds())
	}

	payload := antigravityExternalExecuteRequest{
		Method:                       req.Method,
		URL:                          req.URL.String(),
		Headers:                      req.Header.Clone(),
		BodyBase64:                   base64.StdEncoding.EncodeToString(body),
		ProxyURL:                     strings.TrimSpace(proxyURL),
		ResponseHeaderTimeoutSeconds: timeoutSeconds,
		TLSProfile:                   profile,
	}
	if account != nil {
		payload.AccountConcurrency = account.Concurrency
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal external worker payload: %w", err)
	}

	workerReq, err := http.NewRequest(http.MethodPost, "http://"+e.addr+"/execute", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("create external worker request: %w", err)
	}
	workerReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(workerReq)
	if err != nil {
		return nil, fmt.Errorf("external worker request failed: %w", err)
	}
	if resp.Header.Get("X-Antigravity-Worker-Error") == "1" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("external worker upstream error: %s", strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (e *antigravityExternalWorkerExecutor) ensureStarted() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.client != nil && strings.TrimSpace(e.addr) != "" && e.cmd != nil && e.cmd.Process != nil {
		if e.pingLocked() == nil {
			return nil
		}
		e.resetLocked()
	}

	bin := antigravityExternalWorkerBinaryPath()
	if bin == "" {
		return fmt.Errorf("antigravity external worker binary not configured")
	}

	cmd := exec.Command(bin, "-listen", antigravityExternalWorkerLoopbackTarget)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("external worker stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("external worker stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start external worker: %w", err)
	}

	go io.Copy(io.Discard, stderr)

	announcementCh := make(chan antigravityWorkerAnnouncement, 1)
	errCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil {
			errCh <- readErr
			return
		}
		var announcement antigravityWorkerAnnouncement
		if err := json.Unmarshal(bytes.TrimSpace(line), &announcement); err != nil {
			errCh <- err
			return
		}
		announcementCh <- announcement
		go io.Copy(io.Discard, reader)
	}()

	select {
	case announcement := <-announcementCh:
		if strings.TrimSpace(announcement.Addr) == "" {
			_ = cmd.Process.Kill()
			return fmt.Errorf("external worker returned empty addr")
		}
		e.cmd = cmd
		e.addr = strings.TrimSpace(announcement.Addr)
		e.bin = bin
		e.client = &http.Client{Transport: &http.Transport{DisableCompression: true}}
		return nil
	case readErr := <-errCh:
		_ = cmd.Process.Kill()
		return fmt.Errorf("read external worker startup announcement: %w", readErr)
	case <-time.After(antigravityExternalWorkerStartTimeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("external worker start timeout")
	}
}

func (e *antigravityExternalWorkerExecutor) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked()
}

func (e *antigravityExternalWorkerExecutor) resetLocked() {
	if e.client != nil {
		e.client.CloseIdleConnections()
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_, _ = e.cmd.Process.Wait()
	}
	e.cmd = nil
	e.addr = ""
	e.client = nil
}

func (e *antigravityExternalWorkerExecutor) pingLocked() error {
	if e.client == nil || strings.TrimSpace(e.addr) == "" {
		return fmt.Errorf("external worker not initialized")
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+e.addr+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("external worker unhealthy status=%d", resp.StatusCode)
	}
	return nil
}
