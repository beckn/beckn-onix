package deployconform

import (
	"reflect"
	"testing"
)

// TestVarianceFor verifies artifact-glob matching of variance rules: pattern
// accumulation across rules and whole-artifact marking for empty Paths.
func TestVarianceFor(t *testing.T) {
	tests := []struct {
		name          string
		rules         []VarianceRule
		artifactID    string
		wantPatterns  []string
		wantWholeFlag bool
	}{
		{
			name:         "glob matches yaml under config",
			rules:        []VarianceRule{{Artifacts: []string{"config/*.yaml"}, Paths: []string{"subscriberId"}}},
			artifactID:   "config/a.yaml",
			wantPatterns: []string{"subscriberId"},
		},
		{
			name:       "glob does not match other directory",
			rules:      []VarianceRule{{Artifacts: []string{"config/*.yaml"}, Paths: []string{"subscriberId"}}},
			artifactID: "policies/x.rego",
		},
		{
			name: "multiple matching rules accumulate paths",
			rules: []VarianceRule{
				{Artifacts: []string{"config/*.yaml"}, Paths: []string{"subscriberId"}},
				{Artifacts: []string{"config/a.yaml"}, Paths: []string{"port", "host"}},
			},
			artifactID:   "config/a.yaml",
			wantPatterns: []string{"subscriberId", "port", "host"},
		},
		{
			name:          "empty paths marks whole artifact",
			rules:         []VarianceRule{{Artifacts: []string{"secrets/*"}}},
			artifactID:    "secrets/key.pem",
			wantWholeFlag: true,
		},
		{
			name:         "compose glob matches service artifact",
			rules:        []VarianceRule{{Artifacts: []string{"compose:*"}, Paths: []string{"environment"}}},
			artifactID:   "compose:onix-alpha",
			wantPatterns: []string{"environment"},
		},
		{
			name:       "no rules",
			rules:      nil,
			artifactID: "config/a.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, whole := varianceFor(tt.rules, tt.artifactID)
			if !reflect.DeepEqual(patterns, tt.wantPatterns) {
				t.Fatalf("varianceFor() patterns = %v, want %v", patterns, tt.wantPatterns)
			}
			if whole != tt.wantWholeFlag {
				t.Fatalf("varianceFor() wholeArtifact = %v, want %v", whole, tt.wantWholeFlag)
			}
		})
	}
}

// pluginsTree builds a config tree with a list under "modules" so tests can
// exercise transparent list traversal during redaction.
func pluginsTree(secret string) map[string]any {
	return map[string]any{
		"modules": []any{
			map[string]any{
				"handler": map[string]any{
					"plugins": map[string]any{
						"keyManager": map[string]any{
							"config": map[string]any{"privateKey": secret},
							"id":     "keymanager",
						},
						"cache": map[string]any{
							"config": map[string]any{"addr": "redis:6379"},
						},
					},
				},
				"name": "bapTxnReceiver",
			},
		},
		"appName": "onix",
	}
}

// TestRedactTree verifies placeholder substitution along dot-notation
// patterns, wildcard segments, list traversal, and non-mutation of the input.
func TestRedactTree(t *testing.T) {
	const placeholder = "__PARTICIPANT_SPECIFIC__"

	t.Run("nested path through list is redacted", func(t *testing.T) {
		tree := pluginsTree("secret")
		got := redactTree(tree, []string{"modules.handler.plugins.keyManager.config"}, placeholder)
		module := got.(map[string]any)["modules"].([]any)[0].(map[string]any)
		plugins := module["handler"].(map[string]any)["plugins"].(map[string]any)
		if v := plugins["keyManager"].(map[string]any)["config"]; v != placeholder {
			t.Fatalf("keyManager.config = %v, want placeholder", v)
		}
		// Sibling values are preserved.
		if v := plugins["keyManager"].(map[string]any)["id"]; v != "keymanager" {
			t.Fatalf("keyManager.id = %v, want %q", v, "keymanager")
		}
		if v := plugins["cache"].(map[string]any)["config"].(map[string]any)["addr"]; v != "redis:6379" {
			t.Fatalf("cache.config.addr = %v, want %q", v, "redis:6379")
		}
		if v := module["name"]; v != "bapTxnReceiver" {
			t.Fatalf("module name = %v, want %q", v, "bapTxnReceiver")
		}
	})

	t.Run("wildcard segment matches every key", func(t *testing.T) {
		tree := pluginsTree("secret")
		got := redactTree(tree, []string{"modules.handler.plugins.*.config"}, placeholder)
		module := got.(map[string]any)["modules"].([]any)[0].(map[string]any)
		plugins := module["handler"].(map[string]any)["plugins"].(map[string]any)
		for _, name := range []string{"keyManager", "cache"} {
			if v := plugins[name].(map[string]any)["config"]; v != placeholder {
				t.Fatalf("%s.config = %v, want placeholder", name, v)
			}
		}
	})

	t.Run("terminal match replaces whole subtree", func(t *testing.T) {
		tree := map[string]any{
			"registry": map[string]any{"url": "https://reg", "keys": []any{"k1", "k2"}},
			"other":    "kept",
		}
		got := redactTree(tree, []string{"registry"}, placeholder).(map[string]any)
		if v := got["registry"]; v != placeholder {
			t.Fatalf("registry = %v, want placeholder string", v)
		}
		if v := got["other"]; v != "kept" {
			t.Fatalf("other = %v, want %q", v, "kept")
		}
	})

	t.Run("absent pattern leaves tree unchanged", func(t *testing.T) {
		tree := pluginsTree("secret")
		got := redactTree(tree, []string{"nonexistent.path"}, placeholder)
		if !reflect.DeepEqual(got, tree) {
			t.Fatalf("redactTree() with absent pattern changed the tree:\n got %v\nwant %v", got, tree)
		}
	})

	t.Run("original tree is not mutated", func(t *testing.T) {
		tree := pluginsTree("secret")
		_ = redactTree(tree, []string{"modules.handler.plugins.keyManager.config"}, placeholder)
		module := tree["modules"].([]any)[0].(map[string]any)
		cfg := module["handler"].(map[string]any)["plugins"].(map[string]any)["keyManager"].(map[string]any)["config"]
		if v := cfg.(map[string]any)["privateKey"]; v != "secret" {
			t.Fatalf("original tree mutated: privateKey = %v, want %q", v, "secret")
		}
	})

	t.Run("scalars and list elements preserved otherwise", func(t *testing.T) {
		tree := map[string]any{"items": []any{1, "two", true, nil}, "n": 7}
		got := redactTree(tree, []string{"missing"}, placeholder)
		if !reflect.DeepEqual(got, tree) {
			t.Fatalf("redactTree() altered untouched scalars: got %v, want %v", got, tree)
		}
	})
}

// TestDeepCopy verifies that mutations of a deep copy never reach the
// original nested maps and slices.
func TestDeepCopy(t *testing.T) {
	original := map[string]any{
		"nested": map[string]any{"key": "value"},
		"list":   []any{map[string]any{"item": 1}, "scalar"},
	}
	copied := deepCopy(original).(map[string]any)

	copied["nested"].(map[string]any)["key"] = "changed"
	copied["list"].([]any)[0].(map[string]any)["item"] = 99
	copied["list"].([]any)[1] = "replaced"

	if v := original["nested"].(map[string]any)["key"]; v != "value" {
		t.Fatalf("original nested map mutated: key = %v, want %q", v, "value")
	}
	if v := original["list"].([]any)[0].(map[string]any)["item"]; v != 1 {
		t.Fatalf("original list element map mutated: item = %v, want 1", v)
	}
	if v := original["list"].([]any)[1]; v != "scalar" {
		t.Fatalf("original list scalar mutated: %v, want %q", v, "scalar")
	}
}
