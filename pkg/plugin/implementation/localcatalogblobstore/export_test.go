package localcatalogblobstore

import "context"

// HoldLockForTest takes s's write lock the way PutIfVersion does and
// returns its release.
func HoldLockForTest(s *Store) (func(), error) {
	unlock, err := s.lock(context.Background())
	return func() { _ = unlock() }, err
}

// LockFileName is the lock file's name under Root.
const LockFileName = lockFileName
