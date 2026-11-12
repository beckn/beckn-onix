//go:build unix

package localcatalogblobstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lock takes the exclusive flock on Root's lock file, waiting for other
// holders until ctx ends. Each call opens the file anew, so two calls in
// one process exclude each other exactly as two processes do.
func (s *Store) lock(ctx context.Context) (unlock func(), err error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	// Non-blocking attempts with a short pause rather than a blocking
	// flock, so a waiter can give up when its context ends.
	wait := 200 * time.Microsecond
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < 5*time.Millisecond {
			wait *= 2
		}
	}
}
