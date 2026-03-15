package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
)

const defaultMask = "[MASKED]"

// MaskPIIInPayload returns a copy of the request body with PII values masked
// at the configured dot-paths using dynamic masks (replace or last4).
// Config is loaded from auditFieldsConfig (URL or local path).
// If no config is loaded or piiPaths is empty, the body is returned unchanged.
func MaskPIIInPayload(ctx context.Context, body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	cfg := GetPIIConfig()
	if cfg == nil || len(cfg.Paths) == 0 {
		return body
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		log.Warn(ctx, "failed to unmarshal payload for PII masking")
		return nil
	}

	for _, p := range cfg.Paths {
		parts := strings.Split(p.Path, ".")
		pattern := cfg.Patterns[p.Pattern]
		maskAtPath(root, parts, pattern)
	}

	out, err := json.Marshal(root)
	if err != nil {
		log.Warn(ctx, "failed to marshal PII-masked payload")
		return nil
	}
	return out
}

// maskAtPath walks the JSON tree along the dot-path parts and masks the leaf value.
// Arrays are traversed automatically: if a node is a slice, each element is walked
// with the remaining path segments.
func maskAtPath(cur interface{}, parts []string, pattern *CompiledPattern) {
	if len(parts) == 0 {
		return
	}

	switch node := cur.(type) {
	case map[string]interface{}:
		key := parts[0]
		val, ok := node[key]
		if !ok {
			return
		}
		if len(parts) == 1 {
			node[key] = applyMask(val, pattern)
			return
		}
		maskAtPath(val, parts[1:], pattern)

	case []interface{}:
		for _, elem := range node {
			maskAtPath(elem, parts, pattern)
		}
	}
}

// applyMask replaces the value using the pattern's mask type.
//   - "replace": substitutes the entire value with the pattern's literal mask string.
//   - "last4": keeps the last 4 characters, replaces the rest with '*'.
//
// If the value does not match the pattern's regex, or the pattern is nil,
// the default mask "[MASKED]" is used.
func applyMask(val interface{}, pattern *CompiledPattern) interface{} {
	if pattern == nil {
		return defaultMask
	}

	str, ok := val.(string)
	if !ok {
		str = fmt.Sprintf("%v", val)
	}

	if !pattern.Re.MatchString(str) {
		return defaultMask
	}

	switch pattern.MaskType {
	case "last4":
		return maskLast4(str)
	default:
		if pattern.Mask != "" {
			return pattern.Mask
		}
		return defaultMask
	}
}

// maskLast4 keeps the last 4 characters of s and replaces everything before with '*'.
// If s is 4 characters or shorter, the entire string is replaced with '*'.
func maskLast4(s string) string {
	runes := []rune(s)
	if len(runes) <= 4 {
		return strings.Repeat("*", len(runes))
	}
	masked := strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-4:])
	return masked
}
