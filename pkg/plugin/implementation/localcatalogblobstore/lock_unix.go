//go:build unix

package localcatalogblobstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// errLockNotExclusive reports that another process wrote under Root
// while this one held the lock: the filesystem accepts flock but does
// not enforce it between the processes sharing it.
var errLockNotExclusive = errors.New("another process held the write lock at the same time: this filesystem does not enforce flock between the processes sharing it, so it cannot be shared by several adapters")

// lock takes the exclusive flock on Root's lock file, waiting for other
// holders until ctx ends. Each call opens the file anew, so two calls in
// one process exclude each other exactly as two processes do.
//
// Some filesystems accept flock and enforce nothing between their
// clients (observed: a macOS host directory bind-mounted into two Docker
// Desktop containers). To make that visible rather than silently
// unsafe, the holder writes a random mark into the lock file and unlock
// checks it is still there; a second holder would have replaced it.
// This catches overlapping holders only, so it is a tripwire, not
// protection.
func (s *Store) lock(ctx context.Context) (unlock func() error, err error) {
	lockPath := filepath.Join(s.Root, filepath.FromSlash(lockFileName))
	// The directory can be removed between creating it and opening the
	// file in it (someone emptying the blob root), and some shared
	// filesystems report a directory that was just removed as still
	// there. Creating it again is all that is needed.
	var f *os.File
	for attempt := 0; ; attempt++ {
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			return nil, err
		}
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || attempt == 4 {
			return nil, err
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	// Non-blocking attempts with a short pause rather than a blocking
	// flock, so a waiter can give up when its context ends.
	wait := 200 * time.Microsecond
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
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

	var raw [16]byte
	_, _ = rand.Read(raw[:])
	mark := []byte(hex.EncodeToString(raw[:]))
	if _, err := f.WriteAt(mark, 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("marking the lock file: %w", err)
	}
	return func() error {
		defer f.Close()
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the file releases it anyway
		got := make([]byte, len(mark))
		if _, err := f.ReadAt(got, 0); err != nil || string(got) != string(mark) {
			return errLockNotExclusive
		}
		return nil
	}, nil
}
