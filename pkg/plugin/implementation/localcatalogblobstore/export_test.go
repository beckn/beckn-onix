package localcatalogblobstore

import "context"

// HoldLockForTest takes s's write lock the way PutIfVersion does and
// returns its release.
func HoldLockForTest(s *Store) (func(), error) { return s.lock(context.Background()) }
