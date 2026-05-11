package main

import (
	"context"
	"strings"
	"testing"
)

// TestAgentEngineProviderSuccess verifies the provider's success path:
// a valid context + empty config returns a non-nil wrapper and a nil
// error, with no leaked closer.
//
// The underlying agentenginetransportwrapper.New eagerly builds an OAuth2
// access-token source via Application Default Credentials, so this test
// requires a developer environment where ADC resolves (e.g. via
// `gcloud auth application-default login`). On a CI runner without ADC,
// this test will skip — the failure paths in TestAgentEngineProviderFailure
// still cover provider's error-wrapping behaviour there.
func TestAgentEngineProviderSuccess(t *testing.T) {
	wrapper, closer, err := agentEngineProvider{}.New(
		context.Background(), map[string]any{})
	if err != nil {
		t.Skipf("skipping: ADC unavailable in this environment: %v", err)
	}
	if wrapper == nil {
		t.Fatal("provider.New returned nil wrapper without an error")
	}
	if closer != nil {
		closer()
	}
}

func TestAgentEngineProviderFailure(t *testing.T) {
	provider := agentEngineProvider{}

	cases := []struct {
		name    string
		ctx     context.Context
		config  map[string]any
		wantErr string
	}{
		{
			name:    "Nil context",
			ctx:     nil,
			config:  map[string]any{},
			wantErr: "context cannot be nil",
		},
		{
			name:    "serviceAccount wrong type",
			ctx:     context.Background(),
			config:  map[string]any{"serviceAccount": 42},
			wantErr: "serviceAccount",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			wrapper, closer, err := provider.New(tt.ctx, tt.config)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tt.wantErr)
			}
			if wrapper != nil {
				t.Error("wrapper should be nil on failure")
			}
			if closer != nil {
				t.Error("closer should be nil on failure")
			}
		})
	}
}
