// Package agentenginetransportwrapper transforms outbound Beckn callback
// requests into the Vertex AI Agent Engine :query envelope and injects an
// OAuth2 access token (cloud-platform scope) so the request can hit
// `aiplatform.googleapis.com`. Non-callback actions are forwarded
// unmodified.
package agentenginetransportwrapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
)

// Cloud-platform OAuth2 scope required by *.googleapis.com endpoints.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// callbackActionPrefix marks Beckn callback actions.
// Only actions with this prefix are wrapped into the
// :query envelope and signed with an OAuth2 token.
const callbackActionPrefix = "on_"

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
	ctx context.Context
	tokenSrc oauth2.TokenSource
}

// New parses config, eagerly builds the OAuth2 token source so any auth
// misconfiguration surfaces at startup, and returns a ready Wrapper.
func New(ctx context.Context, config map[string]any) (*Wrapper, func(), error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("agentenginetransportwrapper: context cannot be nil")
	}
	w := &Wrapper{ctx: ctx}
	if v, ok := config["serviceAccount"]; ok {
		w.serviceAccount, ok = v.(string)
		if !ok {
			return nil, nil, fmt.Errorf(
				"agentenginetransportwrapper: config 'serviceAccount' must be a string, got %T", v)
		}
	}

	ts, err := buildTokenSource(ctx, w.serviceAccount)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"agentenginetransportwrapper: build token source: %w", err)
	}
	w.tokenSrc = ts

	return w, nil, nil
}

// buildTokenSource constructs the OAuth2 access TokenSource (cloud-platform
// scope) backed by service-account impersonation when serviceAccount is set,
// otherwise by Application Default Credentials.
func buildTokenSource(ctx context.Context, serviceAccount string) (oauth2.TokenSource, error) {
	if serviceAccount != "" {
		return impersonateOAuth2TokenSource(ctx, serviceAccount, []string{cloudPlatformScope})
	}
	return defaultOAuth2TokenSource(ctx, cloudPlatformScope)
}

// Wrap returns a RoundTripper that transforms callback bodies, injects an
// OAuth2 access token, and forwards to base. Non-callback requests pass
// through unmodified.
func (w *Wrapper) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &aeTransport{
		base:     base,
		tokenSrc: w.tokenSrc,
	}
}

type aeTransport struct {
	base     http.RoundTripper
	tokenSrc oauth2.TokenSource
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

	if !strings.HasPrefix(action, callbackActionPrefix) {
		// Non-callback action: forward the original request unmodified.
		newReq := req.Clone(req.Context())
		setBody(newReq, originalBody)
		return t.base.RoundTrip(newReq)
	}

	wrapped, err := wrapEnvelope(action, originalBody)
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: %w", err)
	}

	// Clone so the caller's request is left untouched (audit logs depend on it).
	newReq := req.Clone(req.Context())
	setBody(newReq, wrapped)
	newReq.Header.Set("Content-Type", "application/json")

	tok, err := fetchTokenWithContext(req.Context(), t.tokenSrc)
	if err != nil {
		return nil, fmt.Errorf("agentenginetransportwrapper: mint token: %w", err)
	}
	newReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	return t.base.RoundTrip(newReq)
}

func fetchTokenWithContext(ctx context.Context, ts oauth2.TokenSource) (*oauth2.Token, error) {
	type result struct {
		tok *oauth2.Token
		err error
	}
	ch := make(chan result, 1)
	go func() {
		tok, err := ts.Token()
		ch <- result{tok, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.tok, r.err
	}
}

func readAndCloseBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}

func setBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
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
