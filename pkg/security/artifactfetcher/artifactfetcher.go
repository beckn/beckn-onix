// Package artifactfetcher performs signed or unsigned, size-capped HTTP GET
// fetches used to walk the manifest -> index -> catalog chain (see
// onix-catalog-crawler-plugin-requirements.md). It is a plain library,
// mirroring pkg/security/artifactverifier, not a plugin.
package artifactfetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// Result is the outcome of a single artifact fetch.
type Result struct {
	Body        []byte
	Digest      string // hex-encoded SHA-256 of Body
	ContentType string
	StatusCode  int
}

// Options controls a single Fetch call.
type Options struct {
	// MaxSize caps the response body size in bytes; a body exceeding this is
	// rejected rather than truncated.
	MaxSize int64
	// Timeout bounds the request, including retries.
	Timeout time.Duration
	// RetryMax is the maximum number of retries on transient failure.
	RetryMax int

	// Signer and KeyManager, when both non-nil, sign the outbound GET with
	// SubscriberID's registered keys (an Authorization header, 3-line
	// signing string, empty-body digest). Leave both nil for an unsigned
	// fetch (e.g. the public .well-known manifest endpoint).
	Signer       definition.Signer
	KeyManager   definition.KeyManager
	SubscriberID string
}

// Fetch performs a size-capped GET against url, optionally signing the
// request first, and returns the body plus its SHA-256 digest. It rejects
// requests to loopback/private-network hosts (SSRF guard) unless the caller
// has otherwise resolved a trusted address.
func Fetch(ctx context.Context, rawURL string, opts Options) (*Result, error) {
	if err := rejectPrivateHost(rawURL); err != nil {
		return nil, err
	}

	client := retryablehttp.NewClient()
	client.Logger = nil
	if opts.Timeout > 0 {
		client.HTTPClient.Timeout = opts.Timeout
	}
	if opts.RetryMax > 0 {
		client.RetryMax = opts.RetryMax
	}

	req, err := retryablehttp.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("artifactfetcher: failed to build request for %s: %w", rawURL, err)
	}
	req = req.WithContext(ctx)

	if opts.Signer != nil && opts.KeyManager != nil {
		if err := sign(ctx, req, opts); err != nil {
			return nil, fmt.Errorf("artifactfetcher: failed to sign request for %s: %w", rawURL, err)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("artifactfetcher: request to %s failed: %w", rawURL, err)
	}
	defer resp.Body.Close()

	maxSize := opts.MaxSize
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}
	body, err := readLimited(resp.Body, maxSize)
	if err != nil {
		return nil, fmt.Errorf("artifactfetcher: reading response from %s: %w", rawURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifactfetcher: %s returned status %s", rawURL, resp.Status)
	}

	digest := sha256.Sum256(body)
	return &Result{
		Body:        body,
		Digest:      hex.EncodeToString(digest[:]),
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
	}, nil
}

const defaultMaxSize = 10 << 20 // 10 MiB, matching manifestloader's cap.

// readLimited reads up to maxSize+1 bytes and errors if the body is larger
// than maxSize, rather than silently truncating it.
func readLimited(r io.Reader, maxSize int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("response body exceeds max size of %d bytes", maxSize)
	}
	return body, nil
}

// sign attaches an Authorization header to req using SubscriberID's
// registered signing key, following the same 3-line signing string and
// header shape core/module/handler's signStep uses for outbound Beckn
// action bodies -- here signed over an empty body, since this is a GET.
func sign(ctx context.Context, req *retryablehttp.Request, opts Options) error {
	keySet, err := opts.KeyManager.Keyset(ctx, opts.SubscriberID)
	if err != nil {
		return fmt.Errorf("failed to get signing key for %s: %w", opts.SubscriberID, err)
	}

	createdAt := time.Now().Unix()
	validTill := time.Now().Add(5 * time.Minute).Unix()
	signature, err := opts.Signer.Sign(ctx, []byte{}, keySet.SigningPrivate, createdAt, validTill)
	if err != nil {
		return err
	}

	authHeader := fmt.Sprintf(
		"Signature keyId=\"%s|%s|ed25519\",algorithm=\"ed25519\",created=\"%d\",expires=\"%d\",headers=\"(created) (expires) digest\",signature=\"%s\"",
		opts.SubscriberID, keySet.UniqueKeyID, createdAt, validTill, signature,
	)
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	return nil
}

// rejectPrivateHost is a basic SSRF guard: it rejects literal loopback and
// private-range IP hosts. It does not resolve DNS names, so a hostname that
// resolves to a private address is not caught here.
func rejectPrivateHost(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("artifactfetcher: invalid URL %q: %w", rawURL, err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		return nil // not a literal IP; DNS-based SSRF guarding is not implemented yet.
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return fmt.Errorf("artifactfetcher: refusing to fetch loopback/private address %s", host)
	}
	return nil
}
