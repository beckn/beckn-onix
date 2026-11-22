package model

import (
	"strings"
	"testing"
)

func TestParseSignatureHeader(t *testing.T) {
	good := `Signature keyId="bap.example.com|k1|ed25519",algorithm="ed25519",created="100",expires="200",headers="(created) (expires) digest",signature="abc+/=="`
	p, err := ParseSignatureHeader(good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for k, want := range map[string]string{
		"keyId": "bap.example.com|k1|ed25519", "algorithm": "ed25519", "created": "100", "expires": "200",
		"headers": "(created) (expires) digest", "signature": "abc+/==",
	} {
		if p[k] != want {
			t.Errorf("%s=%q, want %q", k, p[k], want)
		}
	}

	// Accepted variations: no scheme prefix, spaces after commas, unquoted integers.
	for _, h := range []string{
		`keyId="a|b|ed25519", algorithm="ed25519", created=100, expires=200, signature="x"`,
		`Signature   keyId="a|b|ed25519",algorithm="ed25519",created="1",expires="2",signature="x"`,
	} {
		if _, err := ParseSignatureHeader(h); err != nil {
			t.Errorf("rejected %q: %v", h, err)
		}
	}

	bad := map[string]string{
		"duplicate keyId":           `Signature keyId="attacker|k|ed25519",keyId="victim|k|ed25519",algorithm="ed25519",created="1",expires="2",signature="x"`,
		"duplicate keyId, unquoted": `Signature keyId=attacker|k|ed25519,keyId="victim|k|ed25519",algorithm="ed25519",signature="x"`,
		"duplicate signature":       `Signature keyId="a|k|ed25519",signature="x",signature="y"`,
		"duplicate algorithm":       `Signature keyId="a|k|ed25519",algorithm="ed25519",algorithm="none"`,
		"bare word":                 `Signature keyId="a|k|ed25519",garbage`,
		"junk after value":          `Signature keyId="a|k|ed25519"x,algorithm="ed25519"`,
		"unterminated quote":        `Signature keyId="a|k|ed25519`,
		"trailing comma":            `Signature keyId="a|k|ed25519",`,
		"empty":                     `Signature `,
		"name with quote":           `Signature x"keyId="a|k|ed25519"`,
		"name with space":           `Signature key Id="a|k|ed25519"`,
	}
	for name, h := range bad {
		if p, err := ParseSignatureHeader(h); err == nil {
			t.Errorf("%s: accepted %q as %v", name, h, p)
		}
	}
}

// A parameter whose name merely ends in "keyId" must not be read as keyId by
// anyone. Substring-based readers used to pick it up.
func TestParseSignatureHeaderLookalikeName(t *testing.T) {
	h := `Signature xkeyId="attacker|k|ed25519",keyId="victim|k|ed25519",algorithm="ed25519",created="1",expires="2",signature="x"`
	p, err := ParseSignatureHeader(h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(p["keyId"], "victim|") {
		t.Fatalf("keyId=%q", p["keyId"])
	}
}
