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
	"sort"
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
