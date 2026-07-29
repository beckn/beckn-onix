// Package decode turns already-digest-verified artifact bytes into decoded JSON.
// It is the format extension point: adding zstd/brotli is one entry in the
// registry + one decoder func, with fetch/verify/parse/push untouched. The
// registry is compiled-in and never config-selectable — a decoder inflates
// untrusted bytes, so it is trust surface, not configuration.
package decode

import (
	"bytes"
	"io"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
)

// decoder turns already-digest-verified artifact bytes into a reader over the
// decoded content. It must NOT enforce the size cap — the shared Decode wrapper
// does, so no codec can forget the decompression-bomb guard.
type decoder func(b []byte) (io.ReadCloser, error)

// codecs maps a FileEntry.Encoding value to its decoder.
var codecs = map[string]decoder{
	"":     plainDecoder, // "" and "json" are identity passthrough
	"json": plainDecoder,
	"gzip": gzipDecoder,
	// "zstd": zstdDecoder, // future: one line, nothing else changes
}

func plainDecoder(b []byte) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b)), nil
}

// Decode is the single choke point every fetched artifact goes through: look up
// the codec by encoding, decode, and apply the shared decompressed cap
// (reject-don't-truncate). Unknown encoding or oversize output is permanent.
func Decode(encoding string, b []byte, maxDecompressed int64) ([]byte, error) {
	d, ok := codecs[normalizeEncoding(encoding)]
	if !ok {
		return nil, catalog.Permanentf("crawler: unsupported encoding %q", encoding)
	}
	rc, err := d(b)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	out, err := io.ReadAll(io.LimitReader(rc, maxDecompressed+1))
	if err != nil {
		return nil, catalog.Permanentf("crawler: decode %q: %v", encoding, err)
	}
	// Stays unclassified (=> FaultDecode, "couldn't unpack the files"): a
	// decompression bomb is caught while unpacking, so that IS the truthful stage.
	// FaultOversize is reserved for the download cap in fetch/client.go, whose
	// phrase is "download the files". Both park; only the stage differs.
	if int64(len(out)) > maxDecompressed {
		return nil, catalog.Permanentf("crawler: decoded body exceeds max %d bytes", maxDecompressed)
	}
	return out, nil
}

func normalizeEncoding(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// EncodingFor picks a file's encoding: the explicit FileEntry.Encoding if set,
// else inferred from the URL suffix (.json.gzip / .json.gz => gzip).
func EncodingFor(entryEncoding, url string) string {
	if e := normalizeEncoding(entryEncoding); e != "" {
		return e
	}
	lu := strings.ToLower(url)
	if strings.HasSuffix(lu, ".json.gzip") || strings.HasSuffix(lu, ".json.gz") {
		return "gzip"
	}
	return ""
}
