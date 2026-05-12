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

// marshalEntry serializes a PayloadEntry to a cache-storable string.
// When compress=true: JSON → gzip → base64.
// When compress=false: plain JSON string.
func marshalEntry(entry definition.PayloadEntry, compress bool) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("payloadstore: marshal entry: %w", err)
	}
	if !compress {
		return string(data), nil
	}

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return "", fmt.Errorf("payloadstore: gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("payloadstore: gzip close: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// unmarshalEntry deserializes a string produced by marshalEntry back to a PayloadEntry.
func unmarshalEntry(raw string, compress bool) (definition.PayloadEntry, error) {
	var data []byte
	if compress {
		decoded, err := base64.StdEncoding.DecodeString(raw)
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
	} else {
		data = []byte(raw)
	}

	var entry definition.PayloadEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return definition.PayloadEntry{}, fmt.Errorf("payloadstore: unmarshal entry: %w", err)
	}
	return entry, nil
}
