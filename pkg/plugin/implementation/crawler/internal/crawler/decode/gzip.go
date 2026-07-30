package decode

// gzip.go — the gzip codec: opens a gzip reader over verified bytes (a bad
// header is a permanent error). The shared Decode wrapper, not this decoder,
// enforces the decompressed-size cap.

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
)

func gzipDecoder(b []byte) (io.ReadCloser, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, catalog.Permanentf("crawler: gzip open: %v", err)
	}
	return zr, nil
}
