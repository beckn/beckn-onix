package main

import (
	"context"
	"strings"
	"testing"
)

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
