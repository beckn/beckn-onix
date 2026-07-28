package fetch

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// digestMatches compares body's SHA-256 to a "sha-256:<hex>" digest.
func digestMatches(body []byte, expected string) bool {
	e := strings.ToLower(strings.TrimSpace(expected))
	const prefix = "sha-256:"
	if !strings.HasPrefix(e, prefix) {
		return false
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) == e[len(prefix):]
}
