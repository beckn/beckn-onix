package payloadstore

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

const (
	prefixJSON = "j:"
	prefixGzip = "c:"
)

// marshalEntry serializes a PayloadEntry to a cache-storable string.
// The output is prefixed with a format marker so unmarshalEntry can decode
// it correctly regardless of the current compress config:
//
//	"j:" + plain JSON        (compress=false)
//	"c:" + base64(gzip(JSON)) (compress=true)
func marshalEntry(entry definition.PayloadEntry, compress bool) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("payloadstore: marshal entry: %w", err)
	}
	if !compress {
		return prefixJSON + string(data), nil
	}

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return "", fmt.Errorf("payloadstore: gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("payloadstore: gzip close: %w", err)
	}
	return prefixGzip + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// unmarshalEntry deserializes a string produced by marshalEntry.
// The format is detected from the prefix ("j:" or "c:"), so entries written
// with compress=false can be read back after switching to compress=true and
// vice versa. The compress parameter is unused but kept for API compatibility.
func unmarshalEntry(raw string, _ bool) (definition.PayloadEntry, error) {
	var data []byte
	switch {
	case len(raw) > 2 && raw[:2] == prefixGzip:
		decoded, err := base64.StdEncoding.DecodeString(raw[2:])
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: base64 decode: %w", err)
		}
		r, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: gzip reader: %w", err)
		}
		defer r.Close()
		data, err = io.ReadAll(r)
		if err != nil {
			return definition.PayloadEntry{}, fmt.Errorf("payloadstore: gzip read: %w", err)
		}
	case len(raw) > 2 && raw[:2] == prefixJSON:
		data = []byte(raw[2:])
	default:
		// Legacy entries written before prefix support — treat as plain JSON.
		data = []byte(raw)
	}

	var entry definition.PayloadEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return definition.PayloadEntry{}, fmt.Errorf("payloadstore: unmarshal entry: %w", err)
	}
	return entry, nil
}
