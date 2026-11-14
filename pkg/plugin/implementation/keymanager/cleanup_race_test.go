package keymanager

import (
	"context"
	"sync"
	"testing"

	vault "github.com/hashicorp/vault/api"
)

// The cleanup runs at shutdown, after the HTTP server's grace period; a
// request that outlived the grace period may still be verifying a signature.
// Cleanup must not pull the registry or the Vault client out from under it.
func TestCleanup_ConcurrentWithLookupNPKeys(t *testing.T) {
	orig := getVaultClient
	defer func() { getVaultClient = orig }()
	getVaultClient = func(context.Context, string) (*vault.Client, error) {
		return vault.NewClient(vault.DefaultConfig())
	}

	km, cleanup, err := New(context.Background(), &mockRegistryLookup{}, &Config{VaultAddr: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				if _, _, err := km.LookupNPKeys(context.Background(), "sub", "key"); err != nil {
					t.Errorf("LookupNPKeys during cleanup: %v", err)
					return
				}
			}
		}()
	}
	close(start)
	if err := cleanup(); err != nil {
		t.Errorf("cleanup: %v", err)
	}
	wg.Wait()
}
