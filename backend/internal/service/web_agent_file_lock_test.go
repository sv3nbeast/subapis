//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebAgentFileLockChild(t *testing.T) {
	if os.Getenv("WEB_AGENT_LOCK_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	store, err := NewWebAgentFileStore(os.Getenv("WEB_AGENT_LOCK_ROOT"))
	require.NoError(t, err)
	require.NoError(t, store.WithLock(context.Background(), true, func() error {
		fmt.Println("WEB_AGENT_LOCK_ACQUIRED")
		_, err := io.CopyN(io.Discard, os.Stdin, 1)
		return err
	}))
}
func TestWebAgentFileLockCoordinatesSeparateProcesses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-store")
	store, err := NewWebAgentFileStore(root)
	require.NoError(t, err)
	again, err := NewWebAgentFileStore(root)
	require.NoError(t, err)
	require.Equal(t, store.StorageID(), again.StorageID())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWebAgentFileLockChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), "WEB_AGENT_LOCK_CHILD=1", "WEB_AGENT_LOCK_ROOT="+root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	acquired := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "WEB_AGENT_LOCK_ACQUIRED" {
				acquired <- true
				return
			}
		}
		acquired <- false
	}()
	select {
	case ok := <-acquired:
		require.True(t, ok, "child failed to acquire the lock")
	case <-ctx.Done():
		t.Fatal("child lock acquisition timed out")
	}
	blocked, cancelBlocked := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = store.WithLock(blocked, false, func() error { t.Error("writer passed an exclusive cleanup lock"); return nil })
	cancelBlocked()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = stdin.Write([]byte{1})
	require.NoError(t, err)
	require.NoError(t, stdin.Close())
	require.NoError(t, cmd.Wait(), stderr.String())
	require.NoError(t, store.WithLock(ctx, false, func() error { return nil }))
}
