package config

// enabled_test.go — the opt-in gate: CRAWLER_ENABLED parsing, and that Load
// demands the run-time config (DSN, endpoint, source) only when the crawler is
// actually enabled.

import "testing"

func TestEnabled(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{name: "unset defaults to off", val: "", want: false},
		{name: "true", val: "true", want: true},
		{name: "1", val: "1", want: true},
		{name: "false", val: "false", want: false},
		{name: "0", val: "0", want: false},
		{name: "garbage falls back to off", val: "yes please", want: false},
		{name: "surrounding space is tolerated", val: "  true  ", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Enabled(func(string) string { return tt.val }); got != tt.want {
				t.Fatalf("Enabled(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	full := map[string]string{
		"CRAWLER_ENABLED":       "true",
		"CRAWLER_DB_DSN":        "postgres://u:p@h/db",
		"CRAWLER_PUSH_ENDPOINT": "https://d/push",
		"CRAWLER_INDEX_URLS":    "https://a/i",
	}

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		wantEnabled bool
		wantStore   string
	}{
		{
			name: "disabled needs no config at all",
			env:  nil,
		},
		{
			name: "explicitly disabled ignores a missing DSN",
			env:  map[string]string{"CRAWLER_ENABLED": "false"},
		},
		{
			name:    "enabled without a DSN is an error",
			env:     map[string]string{"CRAWLER_ENABLED": "true", "CRAWLER_PUSH_ENDPOINT": "https://d/push", "CRAWLER_INDEX_URLS": "https://a/i"},
			wantErr: true,
		},
		{
			name:        "enabled with full config",
			env:         full,
			wantEnabled: true,
			wantStore:   DefaultStoreProvider,
		},
		{
			name:        "store provider is selectable",
			env:         merge(full, map[string]string{"CRAWLER_STORE_PROVIDER": "postgres-replica"}),
			wantEnabled: true,
			wantStore:   "postgres-replica",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Load(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if s.Enabled != tt.wantEnabled {
				t.Fatalf("Enabled = %v, want %v", s.Enabled, tt.wantEnabled)
			}
			if s.StoreProvider != tt.wantStore {
				t.Fatalf("StoreProvider = %q, want %q", s.StoreProvider, tt.wantStore)
			}
			if !tt.wantEnabled && s.DBDSN != "" {
				t.Fatalf("disabled settings carry a DSN: %q", s.DBDSN)
			}
		})
	}
}

// merge returns base overlaid with over (base is left untouched).
func merge(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}
