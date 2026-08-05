package deployconform

import (
	"strings"
	"testing"
	"time"
)

// TestCanonicalJSON verifies canonical serialization: sorted keys, compact
// output, nested structures, map[any]any key conversion, and error cases.
func TestCanonicalJSON(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name    string
		input   any
		want    string
		wantErr string
	}{
		{
			name:  "sorted keys compact output",
			input: map[string]any{"b": 1, "a": []any{true, nil, "x"}},
			want:  `{"a":[true,null,"x"],"b":1}`,
		},
		{
			name: "nested maps",
			input: map[string]any{
				"outer": map[string]any{"z": "last", "a": map[string]any{"k": 1}},
			},
			want: `{"outer":{"a":{"k":1},"z":"last"}}`,
		},
		{
			name:  "map any any with string keys",
			input: map[any]any{"b": 2, "a": 1},
			want:  `{"a":1,"b":2}`,
		},
		{
			name:    "map any any with int key",
			input:   map[any]any{1: "one"},
			wantErr: "string mapping keys",
		},
		{
			name:    "unsupported type",
			input:   struct{}{},
			wantErr: "does not support values of type",
		},
		{
			name:  "time value encodes as RFC3339 string",
			input: map[string]any{"at": ts},
			want:  `{"at":"2026-01-02T03:04:05Z"}`,
		},
		{
			name:  "int and float numbers",
			input: map[string]any{"i": 42, "f": 1.5},
			want:  `{"f":1.5,"i":42}`,
		},
		{
			name:  "scalar string",
			input: "hello",
			want:  `"hello"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalJSON(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CanonicalJSON() = %q, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CanonicalJSON() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalJSON() unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("CanonicalJSON() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestNormalizeRaw verifies that CRLF and lone CR line endings normalize to LF.
func TestNormalizeRaw(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "crlf to lf", input: "a\r\nb\r\n", want: "a\nb\n"},
		{name: "lone cr to lf", input: "a\rb", want: "a\nb"},
		{name: "mixed endings", input: "a\r\nb\rc\n", want: "a\nb\nc\n"},
		{name: "already lf unchanged", input: "a\nb\n", want: "a\nb\n"},
		{name: "empty", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(normalizeRaw([]byte(tt.input))); got != tt.want {
				t.Fatalf("normalizeRaw(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSha256Hex verifies the digest against a known SHA-256 test vector.
func TestSha256Hex(t *testing.T) {
	const wantEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := sha256Hex(nil); got != wantEmpty {
		t.Fatalf("sha256Hex(nil) = %q, want %q", got, wantEmpty)
	}
	if got := sha256Hex([]byte("")); got != wantEmpty {
		t.Fatalf(`sha256Hex("") = %q, want %q`, got, wantEmpty)
	}
}

// TestRootHash verifies that the root hash is independent of artifact order
// and sensitive to any per-artifact hash change.
func TestRootHash(t *testing.T) {
	a := BaselineArtifact{ID: "compose:onix-alpha", SHA256: "aaaa"}
	b := BaselineArtifact{ID: "config/onix.yaml", SHA256: "bbbb"}
	c := BaselineArtifact{ID: "policies/x.rego", SHA256: "cccc"}

	ordered := rootHash([]BaselineArtifact{a, b, c})
	shuffled := rootHash([]BaselineArtifact{c, a, b})
	if ordered != shuffled {
		t.Fatalf("rootHash depends on artifact order: %q != %q", ordered, shuffled)
	}

	changed := rootHash([]BaselineArtifact{a, {ID: b.ID, SHA256: "dddd"}, c})
	if changed == ordered {
		t.Fatalf("rootHash did not change when an artifact hash changed")
	}
}
