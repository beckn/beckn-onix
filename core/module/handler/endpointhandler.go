package handler

import (
	"context"
	"net/http"
)

// EndpointHandler is a generic HTTP handler shell: it does no domain-specific work
// itself, only sequencing Decode -> Execute -> Encode. Decode owns every
// HTTP-semantic decision (method check, header/query/body parsing, request
// validation) and always means a malformed/invalid request when it errors,
// surfaced as a transport-level 400. Execute failing means the operation
// ran but failed at a business level; only Encode (endpoint-specific) knows
// how to render that -- e.g. catalog/publish reports it as 200 + a FAILED
// body, not an HTTP error. Encode also receives the originally decoded
// request (not just the response), since an endpoint's rendering may
// legitimately need something only present on the request (e.g.
// catalog/publish's retire list for its bookkeeping) without resorting to a
// shared mutable capture across concurrent requests. This lets multiple
// unrelated plugin-backed endpoints (catalog/publish today, a future
// crawler trigger, etc.) share one handler core with no shared business
// logic.
type EndpointHandler[Req, Resp any] struct {
	Decode  func(ctx context.Context, r *http.Request) (Req, error)
	Execute func(ctx context.Context, req Req) (Resp, error)
	Encode  func(w http.ResponseWriter, r *http.Request, req Req, resp Resp, err error)
}

// ServeHTTP implements http.Handler.
func (e *EndpointHandler[Req, Resp]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := e.Decode(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := e.Execute(r.Context(), req)
	e.Encode(w, r, req, resp, err)
}
