//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package service

import "context"

// Fail closed on runtimes without the supported cross-process storage lock.
func (s *WebAgentFileStore) WithLock(context.Context, bool, func() error) error {
	return ErrWebAgentUnavailable
}
