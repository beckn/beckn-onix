package vcvalidator

import (
	"encoding/json"
	"strings"
)

// extractAction returns the beckn action from the URL path or the request
// body's context.action.
func extractAction(urlPath string, body []byte) string {
	parts := strings.Split(strings.TrimRight(urlPath, "/"), "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if last != "" && last != "caller" && last != "receiver" {
			return last
		}
	}
	var env struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		return env.Context.Action
	}
	return ""
}

// extractCredentials walks the parsed body and returns every embedded
// Verifiable Credential. A credential is recognised as a JSON object that
// carries both a "proof" and a "credentialSubject" — the combination beckn
// uses only for VCs (e.g. participantAttributes holding a
// MeterDataRequestCredential).
func extractCredentials(body []byte) []json.RawMessage {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	var out []json.RawMessage
	walkCredentials(root, &out)
	return out
}

func walkCredentials(node any, out *[]json.RawMessage) {
	switch n := node.(type) {
	case map[string]any:
		if isCredential(n) {
			if b, err := json.Marshal(n); err == nil {
				*out = append(*out, b)
			}
			// A credential's credentialSubject may itself embed nested
			// credentials in other domains; keep walking siblings but not the
			// already-captured subject to avoid double counting.
		}
		for _, v := range n {
			walkCredentials(v, out)
		}
	case []any:
		for _, v := range n {
			walkCredentials(v, out)
		}
	}
}

func isCredential(m map[string]any) bool {
	_, hasProof := m["proof"]
	_, hasSubject := m["credentialSubject"]
	return hasProof && hasSubject
}
