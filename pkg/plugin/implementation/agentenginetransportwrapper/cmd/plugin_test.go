package main

import (
	"context"
	"strings"
	"testing"
)

// TestAgentEngineProviderSuccess verifies that valid configurations produce
// a non-nil TransportWrapper.
func TestAgentEngineProviderSuccess(t *testing.T) {
	provider := agentEngineProvider{}

	cases := []struct {
		name   string
		config map[string]any
	}{
		{
			name:   "Empty config (ADC mode)",
			config: map[string]any{},
		},
		{
			name: "service_account configured (impersonation mode)",
			config: map[string]any{
				"service_account": "sa@p.iam.gserviceaccount.com",
			},
		},
		{
			name: "Unknown keys ignored",
			config: map[string]any{
				"unknown_key": "ignored",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			wrapper, closer, err := provider.New(context.Background(), tt.config)
			if err != nil {
				t.Fatalf("provider.New: unexpected error: %v", err)
			}
			if wrapper == nil {
				t.Fatal("provider.New returned nil wrapper")
			}
			if closer != nil {
				closer()
			}
		})
	}
}

// TestAgentEngineProviderFailure verifies that bad configurations produce
// a clear error and no leaked wrapper / closer.
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
			name:    "service_account wrong type",
			ctx:     context.Background(),
			config:  map[string]any{"service_account": 42},
			wantErr: "service_account",
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
