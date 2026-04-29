package telemetry

import (
	"fmt"
	"strings"
)

const defaultMask = "[MASKED]"

// applyMask replaces val using the pattern's mask type:
//   - "replace": substitutes the entire value with the pattern's literal mask string.
//   - "last4":   keeps the last 4 characters, replaces everything before with '*'.
//
// Non-string values are converted to their string representation before masking.
// A nil pattern returns the default mask "[MASKED]".
func applyMask(val interface{}, pattern *CompiledPattern) interface{} {
	if pattern == nil {
		return defaultMask
	}

	s, ok := val.(string)
	if !ok {
		s = fmt.Sprintf("%v", val)
	}

	switch pattern.MaskType {
	case "last4":
		return maskLast4(s)
	default: // "replace"
		if pattern.Mask != "" {
			return pattern.Mask
		}
		return defaultMask
	}
}

// maskLast4 keeps the last 4 runes of s and replaces everything before with '*'.
// Strings of 4 characters or fewer are fully replaced with '*'.
func maskLast4(s string) string {
	runes := []rune(s)
	if len(runes) <= 4 {
		return strings.Repeat("*", len(runes))
	}
	return strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-4:])
}
