package vcvalidator

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// statusEntry is one credentialStatus descriptor.
type statusEntry struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	StatusPurpose        string `json:"statusPurpose"`
	StatusListIndex      any    `json:"statusListIndex"`
	StatusListCredential string `json:"statusListCredential"`
}

// checkRevocation inspects credentialStatus (object or array) and rejects the
// credential if any revocation entry reports it as revoked.
func (v *verifier) checkRevocation(ctx context.Context, raw json.RawMessage) error {
	entries, err := parseStatusEntries(raw)
	if err != nil {
		return failf(failStructure, "credentialStatus: %v", err)
	}
	for _, e := range entries {
		// Only revocation-purpose entries gate acceptance. Empty purpose is
		// treated as revocation for back-compat with older issuers.
		if e.StatusPurpose != "" && !strings.EqualFold(e.StatusPurpose, "revocation") {
			continue
		}
		revoked, err := v.entryRevoked(ctx, e)
		if err != nil {
			if v.cfg.FailOpen {
				continue
			}
			return failf(failResolution, "revocation check: %v", err)
		}
		if revoked {
			return failf(failRevoked, "credential revoked via %s", e.statusURL())
		}
	}
	return nil
}

func (e statusEntry) statusURL() string {
	if e.StatusListCredential != "" {
		return e.StatusListCredential
	}
	return e.ID
}

func parseStatusEntries(raw json.RawMessage) ([]statusEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []statusEntry
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var one statusEntry
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, err
	}
	return []statusEntry{one}, nil
}

// entryRevoked resolves a single status entry to a revoked/not-revoked verdict.
func (v *verifier) entryRevoked(ctx context.Context, e statusEntry) (bool, error) {
	switch {
	case isDEDI(e):
		return v.dediRevoked(ctx, e)
	case strings.Contains(strings.ToLower(e.Type), "statuslist") && e.StatusListCredential != "":
		return v.statusListRevoked(ctx, e)
	default:
		// Unknown mechanism: fetch the status URL and look for a generic
		// revoked indicator.
		return v.genericRevoked(ctx, e.statusURL())
	}
}

// statusListRevoked implements StatusList2021 / BitstringStatusList lookup:
// fetch the status list credential, gzip-inflate the base64url-encoded
// bitstring, and test the bit at statusListIndex.
func (v *verifier) statusListRevoked(ctx context.Context, e statusEntry) (bool, error) {
	idx, err := toInt(e.StatusListIndex)
	if err != nil {
		return false, fmt.Errorf("statusListIndex: %w", err)
	}
	body, err := v.fetch(ctx, e.StatusListCredential)
	if err != nil {
		return false, err
	}
	var slc struct {
		CredentialSubject struct {
			EncodedList string `json:"encodedList"`
		} `json:"credentialSubject"`
	}
	if err := json.Unmarshal(body, &slc); err != nil {
		return false, fmt.Errorf("parse status list: %w", err)
	}
	if slc.CredentialSubject.EncodedList == "" {
		return false, fmt.Errorf("status list has no encodedList")
	}
	bits, err := decodeBitstring(slc.CredentialSubject.EncodedList)
	if err != nil {
		return false, err
	}
	byteIdx := idx / 8
	bitIdx := uint(idx % 8)
	if byteIdx >= len(bits) {
		return false, nil // index outside list ⇒ not set ⇒ not revoked
	}
	return bits[byteIdx]&(0x80>>bitIdx) != 0, nil
}

// decodeBitstring base64url-decodes then gzip-inflates a status list bitstring.
func decodeBitstring(encoded string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		raw, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode bitstring: %w", err)
		}
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		// some issuers store the bitstring uncompressed.
		return raw, nil
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("inflate bitstring: %w", err)
	}
	return out, nil
}

// isDEDI reports whether a credentialStatus entry references a DEDI revocation
// registry — by type (OpenCred writes "dedi"; older issuers "dediregistry") or
// by a DEDI lookup URL.
func isDEDI(e statusEntry) bool {
	if strings.HasPrefix(strings.ToLower(e.Type), "dedi") {
		return true
	}
	return strings.Contains(strings.ToLower(e.ID), "/dedi/lookup/") ||
		strings.Contains(strings.ToLower(e.StatusListCredential), "/dedi/lookup/")
}

// dediRevoked checks a DEDI revocation registry. DEDI stores ONLY revoked
// records, so the per-credential lookup URL (which embeds the credential hash)
// returns 200 when revoked and 404 when not — record existence, not body
// content, is the signal.
func (v *verifier) dediRevoked(ctx context.Context, e statusEntry) (bool, error) {
	url := e.ID
	if url == "" {
		url = e.StatusListCredential
	}
	if url == "" {
		return false, nil
	}
	status, _, err := v.statusGet(ctx, url)
	if err != nil {
		return false, err // transport failure → caller applies failOpen policy
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return false, nil // no revocation record → not revoked
	case status >= 200 && status < 300:
		return true, nil // record exists → revoked
	default:
		return false, fmt.Errorf("dedi lookup %s: http %d", url, status)
	}
}

// genericRevoked fetches a status document and looks for common revoked
// indicators ("revoked": true, "status": "revoked"/"suspended").
func (v *verifier) genericRevoked(ctx context.Context, url string) (bool, error) {
	if url == "" {
		return false, nil
	}
	body, err := v.fetch(ctx, url)
	if err != nil {
		return false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		// non-JSON body: scan text.
		s := strings.ToLower(string(body))
		return strings.Contains(s, "\"revoked\":true") || strings.Contains(s, "revoked"), nil
	}
	return docRevoked(doc), nil
}

func docRevoked(doc map[string]any) bool {
	for k, val := range doc {
		lk := strings.ToLower(k)
		switch v := val.(type) {
		case bool:
			if lk == "revoked" && v {
				return true
			}
		case string:
			lv := strings.ToLower(v)
			if (lk == "status" || lk == "statuspurpose") && (lv == "revoked" || lv == "suspended") {
				return true
			}
		case map[string]any:
			if docRevoked(v) {
				return true
			}
		}
	}
	return false
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	case string:
		return strconv.Atoi(n)
	case nil:
		return 0, fmt.Errorf("missing")
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}
