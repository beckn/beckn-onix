package opapolicychecker

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// CheckPolicy must keep working while policies are reloaded and swapped: a
// request evaluates against the policy it selected, and the swap must not
// race with evaluation.
func TestCheckPolicy_ConcurrentWithReload(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rego")
	allow := "package policy\nimport rego.v1\ndefault result := {\"valid\": true, \"violations\": []}\n"
	allowV2 := allow + "# reloaded\n"
	if err := os.WriteFile(policyPath, []byte(allow), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	configPath := writeDefaultOnlyNetworkPolicyConfig(t, "type: dir\nlocation: "+dir+"\nquery: data.policy.result\n")
	enforcer, err := New(ctx, map[string]string{"networkPolicyConfig": configPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enforcer.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := enforcer.CheckPolicy(makeStepCtx("confirm", `{"context":{"action":"confirm"}}`)); err != nil {
					t.Errorf("allowed request rejected during reload: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		content := allow
		if i%2 == 0 {
			content = allowV2
		}
		if err := os.WriteFile(policyPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		enforcer.reloadPolicies(context.Background())
	}
	close(stop)
	wg.Wait()
}
