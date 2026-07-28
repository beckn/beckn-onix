package decode

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
)

func gzipDecoder(b []byte) (io.ReadCloser, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, catalog.Permanentf("catalogcrawler: gzip open: %v", err)
	}
	return zr, nil
}
