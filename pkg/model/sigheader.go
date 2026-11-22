package model

import (
	"fmt"
	"strings"
)

// ParseSignatureHeader parses the parameters of a Beckn Authorization /
// Signature header value such as
//
//	Signature keyId="sub|key|ed25519",algorithm="ed25519",created="1",expires="2",headers="(created) (expires) digest",signature="..."
//
// Every component that reads this header must go through this function, so
// that they all agree on what it says. A header that names a parameter twice,
// or that is not a plain list of name="value" pairs, is rejected: a sender
// must not be able to show one keyId to the signature check and another to
// the identity check.
func ParseSignatureHeader(header string) (map[string]string, error) {
	rest := strings.TrimSpace(header)
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "Signature "))
	if rest == "" {
		return nil, fmt.Errorf("signature header is empty")
	}
	params := make(map[string]string)
	for {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("signature header is malformed: expected name=\"value\"")
		}
		name := strings.TrimSpace(rest[:eq])
		if !isSignatureParamName(name) {
			return nil, fmt.Errorf("signature header is malformed: invalid parameter name")
		}
		if _, dup := params[name]; dup {
			return nil, fmt.Errorf("signature header repeats the %q parameter", name)
		}
		rest = rest[eq+1:]

		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("signature header is malformed: unterminated value for %q", name)
			}
			value, rest = rest[1:1+end], rest[end+2:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			value, rest = strings.TrimSpace(rest[:end]), rest[end:]
			if value == "" || strings.ContainsAny(value, "\"= \t") {
				return nil, fmt.Errorf("signature header is malformed: invalid value for %q", name)
			}
		}
		params[name] = value

		rest = strings.TrimSpace(rest)
		if rest == "" {
			return params, nil
		}
		if rest[0] != ',' {
			return nil, fmt.Errorf("signature header is malformed: expected ',' after %q", name)
		}
		rest = strings.TrimSpace(rest[1:])
		if rest == "" {
			return nil, fmt.Errorf("signature header is malformed: trailing ','")
		}
	}
}

func isSignatureParamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
