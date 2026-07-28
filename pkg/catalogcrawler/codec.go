package catalogcrawler

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
)

// PermanentError marks a failure that won't fix itself on retry — an
// unsupported encoding, a decompression bomb, or a corrupt artifact. The
// engine parks these (no hot retry) and never advances the version cursor.
type PermanentError struct{ Msg string }

func (e *PermanentError) Error() string { return e.Msg }

func permanentf(format string, a ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, a...)}
}

// IsPermanent reports whether err (or a wrapped cause) is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// decoder turns already-digest-verified artifact bytes into a reader over the
// decoded content. It must NOT enforce the size cap — the shared decode
// wrapper does, so no codec can forget the decompression-bomb guard.
type decoder func(b []byte) (io.ReadCloser, error)

// codecs maps a FileEntry.Encoding value to its decoder. Adding a format is
// one entry here + one decoder func; fetch/verify/parse/push are untouched.
// The registry is compiled-in and never config-selectable — a decoder inflates
// untrusted bytes, so it is trust surface, not configuration.
var codecs = map[string]decoder{
	"":     plainDecoder, // "" and "json" are identity passthrough
	"json": plainDecoder,
	"gzip": gzipDecoder,
	// "zstd": zstdDecoder, // future: one line, nothing else changes
}

func plainDecoder(b []byte) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b)), nil
}

func gzipDecoder(b []byte) (io.ReadCloser, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, permanentf("catalogcrawler: gzip open: %v", err)
	}
	return zr, nil
}

// decode is the single choke point every fetched artifact goes through: look
// up the codec by encoding, decode, and apply the shared decompressed cap
// (reject-don't-truncate). Unknown encoding or oversize output is permanent.
func decode(encoding string, b []byte, maxDecompressed int64) ([]byte, error) {
	d, ok := codecs[normalizeEncoding(encoding)]
	if !ok {
		return nil, permanentf("catalogcrawler: unsupported encoding %q", encoding)
	}
	rc, err := d(b)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	out, err := io.ReadAll(io.LimitReader(rc, maxDecompressed+1))
	if err != nil {
		return nil, permanentf("catalogcrawler: decode %q: %v", encoding, err)
	}
	if int64(len(out)) > maxDecompressed {
		return nil, permanentf("catalogcrawler: decoded body exceeds max %d bytes", maxDecompressed)
	}
	return out, nil
}

func normalizeEncoding(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// encodingFor picks a file's encoding: the explicit FileEntry.Encoding if set,
// else inferred from the URL suffix (.json.gzip / .json.gz => gzip).
func encodingFor(entryEncoding, url string) string {
	if e := normalizeEncoding(entryEncoding); e != "" {
		return e
	}
	lu := strings.ToLower(url)
	if strings.HasSuffix(lu, ".json.gzip") || strings.HasSuffix(lu, ".json.gz") {
		return "gzip"
	}
	return ""
}
