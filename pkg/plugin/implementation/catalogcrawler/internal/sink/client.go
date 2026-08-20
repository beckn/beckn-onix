package sink

// client.go — HTTP transport that POSTs /push bodies to the
// operator-configured Discovery endpoint, plus the per-batch BatchOutcome
// and the Rollup helper that collapses those outcomes into one SinkOutcome.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// BatchOutcome is the result of pushing one batch of a catalog.
type BatchOutcome struct {
	Acked      bool
	HTTPStatus int
	Reason     string
}

// Client POSTs /push bodies to the (trusted, operator-configured) Discovery
// endpoint. No SSRF guard -- the endpoint is config, not attacker input.
type Client struct{ hc *http.Client }

// NewClient builds a push transport with the given timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{hc: &http.Client{Timeout: timeout}}
}

// Push POSTs a /push body. 200 = accepted; anything else is a non-ack with
// the body as the reason.
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

// Rollup collapses per-batch push outcomes into one crawlmanager.SinkOutcome:
// accepted only if every batch was acked, with the failed batches' reasons
// joined for diagnostics.
func Rollup(outcomes []BatchOutcome) (accepted bool, reason string) {
	var reasons []string
	for _, o := range outcomes {
		if !o.Acked {
			reasons = append(reasons, o.Reason)
		}
	}
	if len(reasons) == 0 {
		return true, ""
	}
	return false, strings.Join(reasons, "; ")
}
