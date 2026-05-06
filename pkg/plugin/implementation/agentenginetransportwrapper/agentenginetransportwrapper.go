// Package agentenginetransportwrapper transforms outbound Beckn requests into
// the Vertex AI Agent Engine :query envelope and injects an OAuth2 access
// token (cloud-platform scope) so the request can hit
// `aiplatform.googleapis.com`.
package agentenginetransportwrapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
)

// Cloud-platform OAuth2 scope required by *.googleapis.com endpoints.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// Package-level factory vars allow tests to substitute fakes.
var (
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return google.DefaultTokenSource(ctx, scopes...)
	}
	impersonateOAuth2TokenSource = func(ctx context.Context, sa string, scopes []string) (oauth2.TokenSource, error) {
		return impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: sa,
			Scopes:          scopes,
		})
	}
)

// Wrapper implements definition.TransportWrapper.
type Wrapper struct {
	serviceAccount string
	ctx            context.Context // for token-source background refresh
}

// New parses config and returns a ready Wrapper.
func New(ctx context.Context, config map[string]any) (*Wrapper, func(), error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("agentenginetransportwrapper: context cannot be nil")
	}
	w := &Wrapper{ctx: ctx}
	if v, ok := config["service_account"]; ok {
		w.serviceAccount, ok = v.(string)
		if !ok {
			return nil, nil, fmt.Errorf(
				"agentenginetransportwrapper: config 'service_account' must be a string, got %T", v)
		}
	}
	return w, nil, nil
}

// Wrap returns a RoundTripper that transforms the body, injects an OAuth2
// access token, and forwards to base.
func (w *Wrapper) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &aeTransport{
		base:           base,
		serviceAccount: w.serviceAccount,
		ctx:            w.ctx,
	}
}

type aeTransport struct {
	base           http.RoundTripper
	serviceAccount string
	ctx            context.Context
	mu             sync.RWMutex
	tokenSrc       oauth2.TokenSource
}

func (t *aeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	originalBody, err := readAndCloseBody(req)
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: failed to read body: %w", err)
	}

	action, err := extractAction(originalBody)
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: %w", err)
	}
	if !strings.HasPrefix(action, "on_") {
		return nil, fmt.Errorf("agentenginetransportwrapper: action %q is not an on_* callback", action)
	}

	wrapped, err := wrapEnvelope(action, originalBody)
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: %w", err)
	}

	// Clone so the caller's request is left untouched (audit logs depend on it).
	newReq := req.Clone(req.Context())
	setBody(newReq, wrapped)
	newReq.Header.Set("Content-Type", "application/json")

	ts, err := t.tokenSource()
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: token source: %w", err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: mint token: %w", err)
	}
	newReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	return t.base.RoundTrip(newReq)
}

// tokenSource returns a cached or freshly-built OAuth2 access TokenSource
// (cloud-platform scope). Used for *.googleapis.com endpoints.
func (t *aeTransport) tokenSource() (oauth2.TokenSource, error) {
	t.mu.RLock()
	if t.tokenSrc != nil {
		ts := t.tokenSrc
		t.mu.RUnlock()
		return ts, nil
	}
	t.mu.RUnlock()

	var (
		ts  oauth2.TokenSource
		err error
	)
	if t.serviceAccount != "" {
		ts, err = impersonateOAuth2TokenSource(t.ctx, t.serviceAccount, []string{cloudPlatformScope})
	} else {
		ts, err = defaultOAuth2TokenSource(t.ctx, cloudPlatformScope)
	}
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokenSrc != nil {
		return t.tokenSrc, nil
	}
	t.tokenSrc = ts
	return ts, nil
}

func readAndCloseBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

// setBody installs body and refreshes the Content-Length on req.
func setBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Del("Content-Length")
}

// agentEngineEnvelope is the body shape Vertex AI Agent Engine's :query expects.
type agentEngineEnvelope struct {
	ClassMethod string           `json:"class_method"`
	Input       agentEngineInput `json:"input"`
}

type agentEngineInput struct {
	Request json.RawMessage `json:"request"`
}

// extractAction returns context.action from a Beckn JSON body.
func extractAction(body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("body is empty")
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("body is not a valid JSON object: %w", err)
	}

	ctxRaw, ok := envelope["context"]
	if !ok {
		return "", fmt.Errorf("body is missing top-level 'context' field")
	}

	var ctxBlock map[string]json.RawMessage
	if err := json.Unmarshal(ctxRaw, &ctxBlock); err != nil {
		return "", fmt.Errorf("'context' is not a JSON object: %w", err)
	}

	actionRaw, ok := ctxBlock["action"]
	if !ok {
		return "", fmt.Errorf("'context.action' field is missing")
	}

	var action string
	if err := json.Unmarshal(actionRaw, &action); err != nil {
		return "", fmt.Errorf("'context.action' is not a JSON string: %w", err)
	}

	if action == "" {
		return "", fmt.Errorf("'context.action' is empty")
	}

	return action, nil
}

// wrapEnvelope builds the Agent Engine :query body, embedding originalBody verbatim.
func wrapEnvelope(action string, originalBody []byte) ([]byte, error) {
	if action == "" {
		return nil, fmt.Errorf("action is empty")
	}
	if !json.Valid(originalBody) {
		return nil, fmt.Errorf("original body is not valid JSON")
	}

	envelope := agentEngineEnvelope{
		ClassMethod: action,
		Input: agentEngineInput{
			Request: json.RawMessage(originalBody),
		},
	}

	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Agent Engine envelope: %w", err)
	}
	return out, nil
}
