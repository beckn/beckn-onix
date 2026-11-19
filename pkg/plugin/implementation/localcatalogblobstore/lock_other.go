//go:build !unix

package localcatalogblobstore

import (
	"context"
	"sync"
)

// Without flock there is no lock other processes would honour: this
// serialises PutIfVersion within one process only, which is what a
// single adapter needs. Sharing one Root between processes is supported
// on unix only.
var processLock sync.Mutex

func (s *Store) lock(ctx context.Context) (unlock func() error, err error) {
	processLock.Lock()
	return func() error { processLock.Unlock(); return nil }, nil
}
