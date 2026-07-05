// Artifact canonicalization: parsed YAML/JSON trees are serialized to compact
// JSON with lexicographically sorted object keys ("canonical-json/1") so that
// hashes depend on configuration content, never on formatting, comments, or
// key order. Raw (non-structured) artifacts are normalized to LF line endings.
package deployconform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// CanonicalJSON serializes a parsed configuration tree to its canonical form:
// compact JSON, object keys sorted lexicographically, values encoded with
// encoding/json. Only types produced by yaml.v3 / encoding/json unmarshaling
// into `any` are accepted; anything else is an error rather than a silent
// best-effort encoding, because both publisher and verifier must agree
// byte-for-byte.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeCanonical appends the canonical encoding of v to buf, recursing into
// maps (sorted by key) and slices (order preserved).
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil, bool, string, int, int64, uint64, float64, time.Time:
		// time.Time appears because yaml.v3 resolves timestamp-shaped scalars;
		// json.Marshal renders it as an RFC 3339 string, which is stable.
		enc, err := json.Marshal(t)
		if err != nil {
			return err
		}
		buf.Write(enc)
	case map[any]any:
		// yaml.v3 produces map[any]any for mappings with non-string keys.
		// Configuration keys must be strings; anything else is rejected so the
		// publisher and verifier can never disagree on key encoding.
		converted := make(map[string]any, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				return fmt.Errorf("canonicalization requires string mapping keys, found %T", k)
			}
			converted[ks] = val
		}
		return writeCanonical(buf, converted)
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(enc)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonicalization does not support values of type %T", v)
	}
	return nil
}

// normalizeRaw converts CRLF and CR line endings to LF so that raw text
// artifacts hash identically across operating systems.
func normalizeRaw(content []byte) []byte {
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(content, []byte("\r"), []byte("\n"))
}

// sha256Hex returns the lowercase hex SHA-256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// rootHash commits to a full artifact list: the SHA-256 over the sorted
// "id sha256" lines of every artifact. A single comparison of root hashes
// answers "is this deployment conformant" before any per-artifact work.
func rootHash(artifacts []BaselineArtifact) string {
	lines := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		lines = append(lines, a.ID+" "+a.SHA256+"\n")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, line := range lines {
		h.Write([]byte(line))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// Redaction of participant-owned configuration before hashing. The network
// facilitator declares variance rules (which paths of which artifacts are
// legitimately participant-specific — keys, identities, ports, registry
// details); the functions below replace the matched values with a fixed
// placeholder so that every compliant participant hashes to the same value
// and no secret material ever enters a baseline, log line, or telemetry
// event.
// ---------------------------------------------------------------------------

// varianceFor collects the path patterns of every variance rule whose
// artifact glob matches artifactID. The second result is true when a matching
// rule has no paths, which marks the whole artifact participant-owned.
func varianceFor(rules []VarianceRule, artifactID string) (patterns []string, wholeArtifact bool) {
	for _, rule := range rules {
		for _, glob := range rule.Artifacts {
			ok, err := path.Match(glob, artifactID)
			if err != nil || !ok {
				continue
			}
			if len(rule.Paths) == 0 {
				wholeArtifact = true
			}
			patterns = append(patterns, rule.Paths...)
			break
		}
	}
	return patterns, wholeArtifact
}

// redactTree returns a deep copy of tree with every value matched by one of
// the dot-notation patterns replaced by placeholder. The original tree is
// never mutated: the unredacted form is still needed as Rego policy input.
func redactTree(tree any, patterns []string, placeholder string) any {
	copied := deepCopy(tree)
	for _, pattern := range patterns {
		segments := strings.Split(pattern, ".")
		redactPath(copied, segments, placeholder)
	}
	return copied
}

// redactPath walks node along segments and replaces each terminal match with
// placeholder. Map keys match a segment literally or via the "*" wildcard;
// list elements are traversed transparently (a pattern never mentions list
// indices, it applies to every element).
func redactPath(node any, segments []string, placeholder string) {
	if len(segments) == 0 {
		return
	}
	switch t := node.(type) {
	case []any:
		for _, item := range t {
			redactPath(item, segments, placeholder)
		}
	case map[string]any:
		seg := segments[0]
		for key := range t {
			if seg != "*" && seg != key {
				continue
			}
			if len(segments) == 1 {
				t[key] = placeholder
			} else {
				redactPath(t[key], segments[1:], placeholder)
			}
		}
	}
}

// deepCopy clones the tree shapes produced by YAML/JSON unmarshaling into
// `any`. Scalars are immutable and returned as-is.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		copied := make(map[string]any, len(t))
		for k, val := range t {
			copied[k] = deepCopy(val)
		}
		return copied
	case []any:
		copied := make([]any, len(t))
		for i, val := range t {
			copied[i] = deepCopy(val)
		}
		return copied
	default:
		return v
	}
}
