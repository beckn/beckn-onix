package publish

// client.go — HTTP transport that POSTs /push bodies to the operator-configured
// Discovery endpoint, plus the per-batch BatchOutcome and the Rollup/AckedCount
// helpers that collapse those outcomes into a catalog-level sync status.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// crawler_index.sync_status values (matches the design's CHECK constraint):
// initial, every part landed, some landed, or none.
const (
	SyncPending = "pending"
	SyncOK      = "ok"
	SyncPartial = "partial"
	SyncFailed  = "failed"
)

// BatchOutcome is the result of pushing one batch of a catalog. (was PartOutcome)
type BatchOutcome struct {
	Acked      bool
	HTTPStatus int
	Reason     string
}

// Client POSTs /push bodies to the (trusted, operator-configured) Discovery
// endpoint. No SSRF guard — the endpoint is config, not attacker input.
type Client struct{ hc *http.Client }

// NewClient builds a push transport with the given timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{hc: &http.Client{Timeout: timeout}}
}

// Push POSTs a /push body. 200 = accepted; anything else is a non-ack with the
// body as the reason.
func (c *Client) Push(ctx context.Context, endpoint string, body []byte) (BatchOutcome, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return BatchOutcome{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return BatchOutcome{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	out := BatchOutcome{Acked: resp.StatusCode == http.StatusOK, HTTPStatus: resp.StatusCode}
	if !out.Acked {
		out.Reason = strings.TrimSpace(string(respBody))
	}
	return out, nil
}

// Rollup collapses per-batch push outcomes into a catalog-level sync_status and
// returns the batches that failed (for retry + the error reason).
func Rollup(outcomes []BatchOutcome) (status string, failed []BatchOutcome) {
	acked := 0
	for _, o := range outcomes {
		if o.Acked {
			acked++
		} else {
			failed = append(failed, o)
		}
	}
	switch {
	case len(failed) == 0:
		return SyncOK, nil
	case acked == 0:
		return SyncFailed, failed
	default:
		return SyncPartial, failed
	}
}

// AckedCount is how many pushed batches Discovery acknowledged.
func AckedCount(outcomes []BatchOutcome) int {
	n := 0
	for _, o := range outcomes {
		if o.Acked {
			n++
		}
	}
	return n
}
