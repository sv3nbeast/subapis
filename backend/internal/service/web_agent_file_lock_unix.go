//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Permanent lock inode: never unlink it. Shared writers and exclusive cleanup
// are coordinated across processes, even if their database contexts expire.
func (s *WebAgentFileStore) WithLock(ctx context.Context, exclusive bool, fn func() error) error {
	path := filepath.Join(s.root, ".web-agent-store.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	fileInfo, err := f.Stat()
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) || pathInfo.Mode().Perm()&0077 != 0 {
		return ErrWebAgentInvalid
	}
	operation := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		operation = unix.LOCK_EX | unix.LOCK_NB
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = unix.Flock(int(f.Fd()), operation)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}
