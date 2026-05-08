// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"strings"
	"testing"
)

// TestAgentEngineProviderFailure verifies that bad configurations produce
// a clear error and no leaked wrapper / closer at the provider boundary.
//
// The success path is exercised in package-level tests in
// agentenginetransportwrapper_test.go which can install fake token-source
// factories. The cmd test focuses on the provider's wrapping behaviour for
// errors (true-nil interface return on failure, error message preservation).
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
		{
			name:    "allowedActionPrefixes wrong type",
			ctx:     context.Background(),
			config:  map[string]any{"allowedActionPrefixes": "on_"},
			wantErr: "allowedActionPrefixes",
		},
		{
			name:    "passthroughOther wrong type",
			ctx:     context.Background(),
			config:  map[string]any{"passthroughOther": "true"},
			wantErr: "passthroughOther",
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
