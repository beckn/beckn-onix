package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// DefaultMaxBodyBytes is the limit applied to a request body, and to an
// upstream response body, when none is configured.
const DefaultMaxBodyBytes int64 = 10 << 20

// maxResponseBodyBytes returns the configured upstream response limit, or
// the default when none is set.
func (c *HttpClientConfig) maxResponseBodyBytes() int64 {
	if c == nil || c.MaxResponseBodyBytes <= 0 {
		return DefaultMaxBodyBytes
	}
	return c.MaxResponseBodyBytes
}

// RejectBodyTooLarge answers a request whose body is over limit with a 413
// NACK and logs who sent it. Every route uses it, so a sender sees the same
// response wherever the limit is hit. The body was not read, so the protocol
// version is unknown and the NACK has the pre-v2 shape.
func RejectBodyTooLarge(w http.ResponseWriter, r *http.Request, limit int64) {
	log.Warnf(r.Context(), "rejected request body over the %d byte limit: method=%s path=%s remoteAddr=%s contentLength=%d",
		limit, r.Method, r.URL.Path, r.RemoteAddr, r.ContentLength)
	w.Header().Set("Connection", "close")
	sendNack(r.Context(), w, model.NewCodedErr(http.StatusRequestEntityTooLarge, "SCH_INVALID_FORMAT",
		fmt.Errorf("request body exceeds the %d byte limit", limit)))
}

// readRequestBody reads the whole request body. With a declared length the
// result ends up in one allocation of exactly that size, grown as the bytes
// arrive so a sender cannot reserve memory by declaring a length and sending
// nothing.
func readRequestBody(r *http.Request) ([]byte, error) {
	declared := r.ContentLength
	if declared <= 0 {
		return io.ReadAll(r.Body)
	}
	const firstChunk = 64 << 10
	buf := make([]byte, 0, min(declared, firstChunk))
	for {
		if len(buf) == cap(buf) {
			if int64(cap(buf)) >= declared {
				// Everything declared has arrived; anything further is the
				// transport's to report.
				extra, err := io.ReadAll(r.Body)
				return append(buf, extra...), err
			}
			grown := make([]byte, len(buf), min(declared, int64(cap(buf))*2))
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Body.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}
