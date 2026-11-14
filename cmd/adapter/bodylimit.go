package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// defaultMaxBodyBytes is the request body limit used when http.maxBodyBytes
// is not set.
const defaultMaxBodyBytes int64 = 10 << 20

// maxBodyBytes returns the configured request body limit, or the default
// when none is set.
func (c httpConfig) maxBodyBytes() int64 {
	if c.MaxBodyBytes <= 0 {
		return defaultMaxBodyBytes
	}
	return c.MaxBodyBytes
}

// limitRequestBody caps the size of every request body before any handler or
// middleware reads it. The body is read, and logged, ahead of the signature
// check, so without a cap an unauthenticated sender decides how much memory a
// request costs.
//
// A request that declares a larger Content-Length is refused unread. One that
// declares no length is cut off at the limit: the read fails with
// *http.MaxBytesError, which the handlers answer with 413.
func limitRequestBody(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			data, _ := json.Marshal(model.NewCodedError("SCH_INVALID_FORMAT",
				fmt.Sprintf("request body of %d bytes exceeds the %d byte limit", r.ContentLength, limit)))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write(data)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
