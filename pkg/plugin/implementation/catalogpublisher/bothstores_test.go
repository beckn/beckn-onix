package catalogpublisher

// bothstores_test.go repeats this package's end-to-end tables -- the
// "latest" lifecycle, verification behind a cache, the identity rules at
// the HTTP boundary -- over a blob store with a conditional write.
// catalog-core's Store has two write paths, chosen by what the blob store
// can do, and the adapter's own localcatalogblobstore takes the
// conditional one. The in-memory double the tables were written against
// takes the plain one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// worldConditional selects what worldStore hands out. The tests that set
// it do not run in parallel with anything.
var worldConditional bool

// worldStore is how every test world in this package hands its in-memory
// blob store to New.
func worldStore(bs *fakeBlobStore) definition.CatalogBlobStore {
	if worldConditional {
		return conditionalFake{bs}
	}
	return bs
}

// conditionalFake is fakeBlobStore plus definition.ConditionalCatalogBlobStore.
// A blob's version is the hash of its content.
type conditionalFake struct{ *fakeBlobStore }

func fakeVersion(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (c conditionalFake) GetVersioned(ctx context.Context, path string) ([]byte, string, error) {
	b, err := c.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return b, fakeVersion(b), nil
}

func (c conditionalFake) PutIfVersion(_ context.Context, path string, content []byte, ifVersion string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := ""
	if b, ok := c.data[path]; ok {
		current = fakeVersion(b)
	}
	if current != ifVersion {
		return "", definition.ErrBlobPreconditionFailed
	}
	c.data[path] = content
	return fakeVersion(content), nil
}

var _ definition.ConditionalCatalogBlobStore = conditionalFake{}

func TestOnConditionalStore(t *testing.T) {
	worldConditional = true
	defer func() { worldConditional = false }()
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"Lifecycle_LatestStaysConsistent", TestLifecycle_LatestStaysConsistent},
		{"Verify_LatestServedStale", TestVerify_LatestServedStale},
		// Not TestVerify_ImmutableFileMismatchIsNotRetried: it plants other
		// bytes at the next change file's path and asserts on elapsed time.
		// On this path the store first waits its take-over grace (250ms)
		// before replacing a versioned file it did not write, which the
		// test cannot tell from a verification retry. What it checks, the
		// error class, does not depend on the write path.
		{"Verify_StaleIndexIsNotAccepted", TestVerify_StaleIndexIsNotAccepted},
		{"Verify_UnchangedOutcomeIsVerified", TestVerify_UnchangedOutcomeIsVerified},
		{"Verify_RepeatRetireIsVerified", TestVerify_RepeatRetireIsVerified},
		{"Verify_RepeatRetireRepairsLatestLeftLive", TestVerify_RepeatRetireRepairsLatestLeftLive},
		{"HandlerIdentityDecisionTable", TestHandlerIdentityDecisionTable},
		{"HandlerIdentity_RetireUnknownIsRejectedNotRetired", TestHandlerIdentity_RetireUnknownIsRejectedNotRetired},
		{"HandlerIdentity_MixedBatch", TestHandlerIdentity_MixedBatch},
		{"HandlerIdentity_SharedLastSegment", TestHandlerIdentity_SharedLastSegment},
		{"HandlerIdentity_SameIdInCatalogsAndRetire", TestHandlerIdentity_SameIdInCatalogsAndRetire},
		{"HandlerIdentity_SameIdTwiceInRetire", TestHandlerIdentity_SameIdTwiceInRetire},
		{"HandlerIdentity_MetadataIsRestatedOnEveryPublish", TestHandlerIdentity_MetadataIsRestatedOnEveryPublish},
		{"Publish_UnchangedRepublishKeepsLatestConsistent", TestPublish_UnchangedRepublishKeepsLatestConsistent},
		{"Publish_ConcurrentCallsKeepEveryCatalog", TestPublish_ConcurrentCallsKeepEveryCatalog},
	} {
		t.Run(tc.name, tc.run)
	}
}
