package simplekeymanager

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Keyset is called by every signing request. InsertKeyset and DeleteKeyset
// are part of the same definition.KeyManager interface, so they must be safe
// to call while requests are being signed. On an unguarded map this is a data
// race, and at runtime "fatal error: concurrent map read and map write",
// which no recover() can catch.
func TestKeysets_ConcurrentInsertDeleteAndRead(t *testing.T) {
	skm, cleanup, err := New(context.Background(), &mockRegistry{}, &Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if err := skm.InsertKeyset(ctx, "stable", &model.Keyset{UniqueKeyID: "k0", SigningPrivate: "p0"}); err != nil {
		t.Fatal(err)
	}

	const workers, rounds = 8, 300
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func(w int) { // readers: the signing path
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				ks, err := skm.Keyset(ctx, "stable")
				if err != nil || ks.UniqueKeyID != "k0" {
					t.Errorf("Keyset(stable) = %+v, %v", ks, err)
					return
				}
			}
		}(w)
		go func(w int) { // writers: key rotation
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				id := fmt.Sprintf("rot-%d-%d", w, i)
				if err := skm.InsertKeyset(ctx, id, &model.Keyset{UniqueKeyID: id}); err != nil {
					t.Errorf("InsertKeyset: %v", err)
					return
				}
				if err := skm.DeleteKeyset(ctx, id); err != nil {
					t.Errorf("DeleteKeyset: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Shutdown while a request is still signing must not crash it either.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_, _ = skm.Keyset(ctx, "stable")
		}
	}()
	if err := cleanup(); err != nil {
		t.Errorf("cleanup: %v", err)
	}
	wg.Wait()
}
