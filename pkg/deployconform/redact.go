// Redaction of participant-owned configuration before hashing. The network
// facilitator declares variance rules (which paths of which artifacts are
// legitimately participant-specific — keys, identities, ports, registry
// details); this file replaces the matched values with a fixed placeholder so
// that every compliant participant hashes to the same value and no secret
// material ever enters a baseline, log line, or telemetry event.
package deployconform

import (
	"path"
	"strings"
)

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
