package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkerRunsPythonWithCapturedOutput(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	w := &worker{pythonPath: python, timeout: 3 * time.Second, maxCodeBytes: 1024, maxOutputBytes: 1024}
	result := w.run(context.Background(), "print('HELLO_CHECK')")
	require.Empty(t, result.ErrorCode)
	require.Equal(t, 0, result.ReturnCode)
	require.Equal(t, "HELLO_CHECK\n", result.Stdout)
	require.Empty(t, result.Stderr)
}

func TestWorkerTerminatesTimedOutPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	w := &worker{pythonPath: python, timeout: 100 * time.Millisecond, maxCodeBytes: 1024, maxOutputBytes: 1024}
	result := w.run(context.Background(), "while True:\n    pass\n")
	require.Equal(t, "execution_time_exceeded", result.ErrorCode)
	require.Equal(t, 124, result.ReturnCode)
}

func TestCappedBufferBoundsOutput(t *testing.T) {
	buf := newCappedBuffer(4)
	n, err := buf.Write([]byte("abcdefgh"))
	require.NoError(t, err)
	require.Equal(t, 8, n)
	require.Contains(t, buf.Output(), "abcd")
	require.Contains(t, buf.Output(), "output truncated")
}

func TestPrepareSocketRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sock")
	require.NoError(t, os.WriteFile(path, []byte("do not delete"), 0o600))
	require.Error(t, prepareSocket(path))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "do not delete", string(content))
}
