package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultSocketPath     = "/app/data/worker.sock"
	defaultMaxCodeBytes   = 128 << 10
	defaultMaxOutputBytes = 256 << 10
	defaultRequestBytes   = 192 << 10
)

type executeRequest struct {
	Code string `json:"code"`
}

type executeResponse struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ReturnCode int    `json:"return_code"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type worker struct {
	pythonPath     string
	timeout        time.Duration
	maxCodeBytes   int
	maxOutputBytes int
}

func main() {
	socketPath := flag.String("socket", defaultSocketPath, "Unix-domain socket path")
	pythonPath := flag.String("python", "/usr/bin/python3", "isolated Python interpreter")
	timeout := flag.Duration("timeout", 15*time.Second, "per-execution wall-clock timeout")
	maxCodeBytes := flag.Int("max-code-bytes", defaultMaxCodeBytes, "maximum Python source bytes")
	maxOutputBytes := flag.Int("max-output-bytes", defaultMaxOutputBytes, "maximum bytes for each output stream")
	flag.Parse()

	if err := prepareSocket(*socketPath); err != nil {
		fmt.Fprintf(os.Stderr, "prepare socket: %v\n", err)
		os.Exit(1)
	}
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(*socketPath)
	}()
	if err := os.Chmod(*socketPath, 0o660); err != nil {
		fmt.Fprintf(os.Stderr, "chmod socket: %v\n", err)
		os.Exit(1)
	}

	runtime := &worker{
		pythonPath:     *pythonPath,
		timeout:        *timeout,
		maxCodeBytes:   *maxCodeBytes,
		maxOutputBytes: *maxOutputBytes,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", runtime.health)
	mux.HandleFunc("/execute", runtime.execute)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      *timeout + 5*time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

func prepareSocket(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("empty socket path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to remove non-socket path %s", path)
		}
		return os.Remove(path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (w *worker) health(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(rw, `{"status":"ok"}`)
}

func (w *worker) execute(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req.Body = http.MaxBytesReader(rw, req.Body, defaultRequestBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	var payload executeRequest
	if err := decoder.Decode(&payload); err != nil {
		http.Error(rw, "invalid payload", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Code) == "" || len(payload.Code) > w.maxCodeBytes {
		http.Error(rw, "invalid code", http.StatusBadRequest)
		return
	}

	result := w.run(req.Context(), payload.Code)
	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(result); err != nil {
		return
	}
}

func (w *worker) run(parent context.Context, code string) executeResponse {
	runCtx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "sub2api-code-")
	if err != nil {
		return unavailableResult(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Chmod(dir, 0o700); err != nil {
		return unavailableResult(err)
	}
	script := filepath.Join(dir, "main.py")
	if err := os.WriteFile(script, []byte(code), 0o600); err != nil {
		return unavailableResult(err)
	}

	stdout := newCappedBuffer(w.maxOutputBytes)
	stderr := newCappedBuffer(w.maxOutputBytes)
	cmd := exec.CommandContext(runCtx, w.pythonPath, "-I", "-S", "-u", script)
	cmd.Dir = dir
	cmd.Env = []string{
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"PATH=/usr/bin:/bin",
		"LANG=C.UTF-8",
		"PYTHONIOENCODING=utf-8",
		"PYTHONDONTWRITEBYTECODE=1",
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second

	err = cmd.Run()
	result := executeResponse{Stdout: stdout.Output(), Stderr: stderr.Output()}
	if runCtx.Err() == context.DeadlineExceeded {
		result.ReturnCode = 124
		result.ErrorCode = "execution_time_exceeded"
		return result
	}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ReturnCode = exitErr.ExitCode()
		return result
	}
	return unavailableResult(err)
}

func unavailableResult(err error) executeResponse {
	message := "execution worker unavailable"
	if err != nil {
		message += ": " + err.Error()
	}
	return executeResponse{Stderr: message, ReturnCode: 1, ErrorCode: "unavailable"}
}

type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return original, nil
}

func (b *cappedBuffer) Output() string {
	if !b.truncated {
		return b.buf.String()
	}
	return b.buf.String() + "\n[output truncated at " + strconv.Itoa(b.limit) + " bytes]\n"
}
