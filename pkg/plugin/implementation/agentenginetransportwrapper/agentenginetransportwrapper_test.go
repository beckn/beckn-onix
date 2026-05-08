package agentenginetransportwrapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeTokenSource produces a fixed access token until refreshed.
type fakeTokenSource struct {
	token string
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: f.token,
		Expiry:      time.Now().Add(time.Hour),
	}, nil
}

// installFakeTokens swaps the OAuth2 token-source factories for the test's
// lifetime. ADC and impersonation paths return distinct tokens so callers
// can distinguish which path was taken. Must be called BEFORE New() because
// New() builds the token source eagerly.
func installFakeTokens(t *testing.T, token string) {
	t.Helper()
	origDef := defaultOAuth2TokenSource
	origImp := impersonateOAuth2TokenSource
	t.Cleanup(func() {
		defaultOAuth2TokenSource = origDef
		impersonateOAuth2TokenSource = origImp
	})
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return &fakeTokenSource{token: token}, nil
	}
	impersonateOAuth2TokenSource = func(ctx context.Context, sa string, scopes []string) (oauth2.TokenSource, error) {
		return &fakeTokenSource{token: token + "#" + sa}, nil
	}
}

// recordingServer is an httptest.Server that records the last request served.
type recordingServer struct {
	*httptest.Server
	last   atomic.Value // holds *recordedRequest
	status int          // 0 => 200
}

type recordedRequest struct {
	Method string
	Header http.Header
	Body   []byte
}

func newRecordingServer(t *testing.T, status int) *recordingServer {
	t.Helper()
	rs := &recordingServer{status: status}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		rs.last.Store(&recordedRequest{
			Method: r.Method,
			Header: r.Header.Clone(),
			Body:   body,
		})
		s := rs.status
		if s == 0 {
			s = http.StatusOK
		}
		w.WriteHeader(s)
		_, _ = w.Write([]byte(`{"message":{"ack":{"status":"ACK"}}}`))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) Last() *recordedRequest {
	v := rs.last.Load()
	if v == nil {
		return nil
	}
	return v.(*recordedRequest)
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// ---- New() ---------------------------------------------------------------

func TestNew_RejectsNilContext(t *testing.T) {
	installFakeTokens(t, "tok")
	_, _, err := New(nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Errorf("expected nil-context error, got %v", err)
	}
}

func TestNew_RejectsWrongTypedConfigValues(t *testing.T) {
	installFakeTokens(t, "tok")
	t.Run("serviceAccount non-string", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"serviceAccount": 0,
		})
		if err == nil || !strings.Contains(err.Error(), "serviceAccount") {
			t.Errorf("expected error mentioning 'serviceAccount', got %v", err)
		}
	})
	t.Run("absent keys default to empty", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error for empty config: %v", err)
		}
		if w.serviceAccount != "" {
			t.Errorf("serviceAccount should default to empty, got %q", w.serviceAccount)
		}
	})
}

// TestNew_BuildsTokenSourceEagerly verifies that auth misconfiguration is
// surfaced at adapter startup, not at first callback.
func TestNew_BuildsTokenSourceEagerly(t *testing.T) {
	t.Run("ADC factory called once at New", func(t *testing.T) {
		var calls int32
		origDef := defaultOAuth2TokenSource
		defer func() { defaultOAuth2TokenSource = origDef }()
		defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
			atomic.AddInt32(&calls, 1)
			return &fakeTokenSource{token: "t"}, nil
		}
		w, _, err := New(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("ADC factory calls during New = %d, want 1", got)
		}
		if w.tokenSrc == nil {
			t.Error("Wrapper.tokenSrc should be populated by New()")
		}
	})
	t.Run("impersonation factory called when SA configured", func(t *testing.T) {
		var calls int32
		origImp := impersonateOAuth2TokenSource
		defer func() { impersonateOAuth2TokenSource = origImp }()
		impersonateOAuth2TokenSource = func(ctx context.Context, sa string, scopes []string) (oauth2.TokenSource, error) {
			atomic.AddInt32(&calls, 1)
			return &fakeTokenSource{token: "imp:" + sa}, nil
		}
		_, _, err := New(context.Background(), map[string]any{
			"serviceAccount": "sa@p.iam.gserviceaccount.com",
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("impersonation factory calls during New = %d, want 1", got)
		}
	})
	t.Run("token source error fails New", func(t *testing.T) {
		origDef := defaultOAuth2TokenSource
		defer func() { defaultOAuth2TokenSource = origDef }()
		defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
			return nil, errors.New("ADC unavailable")
		}
		_, _, err := New(context.Background(), map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "ADC unavailable") {
			t.Errorf("expected wrapped ADC error from New, got %v", err)
		}
	})
}

// ---- Wrap() --------------------------------------------------------------

func TestWrap_WithNilBaseUsesDefault(t *testing.T) {
	w := &Wrapper{
		ctx:      context.Background(),
		tokenSrc: &fakeTokenSource{token: "t"},
	}
	rt := w.Wrap(nil)
	if rt == nil {
		t.Fatal("Wrap returned nil")
	}
	at, ok := rt.(*aeTransport)
	if !ok {
		t.Fatalf("Wrap returned %T, want *aeTransport", rt)
	}
	if at.base == nil {
		t.Error("aeTransport.base should default to http.DefaultTransport, got nil")
	}
}

// ---- RoundTrip() ---------------------------------------------------------

func TestRoundTrip_HappyPath_BodyAndHeader(t *testing.T) {
	installFakeTokens(t, "test-access-token")

	srv := newRecordingServer(t, http.StatusOK)
	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"on_discover"},"message":{"catalogs":[]}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/x:query",
		bytes.NewReader(body))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)

	last := srv.Last()
	if last == nil {
		t.Fatal("server received no request")
	}
	if got := last.Header.Get("Authorization"); got != "Bearer test-access-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-access-token")
	}
	if got := last.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	// Verify body was wrapped into the Agent Engine envelope.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(last.Body, &env); err != nil {
		t.Fatalf("server body is not JSON: %v", err)
	}
	wantTopKeys := map[string]bool{"class_method": true, "input": true}
	for _, k := range keys(env) {
		if !wantTopKeys[k] {
			t.Errorf("unexpected top-level key %q in envelope", k)
		}
	}
	var classMethod string
	if err := json.Unmarshal(env["class_method"], &classMethod); err != nil {
		t.Fatalf("class_method not a string: %v", err)
	}
	if classMethod != "on_discover" {
		t.Errorf("class_method = %q, want on_discover", classMethod)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(env["input"], &input); err != nil {
		t.Fatalf("input not an object: %v", err)
	}
	if !bytes.Equal(input["request"], body) {
		t.Errorf("input.request = %s, want %s", input["request"], body)
	}
}

func TestRoundTrip_AllSevenOnActions(t *testing.T) {
	installFakeTokens(t, "tok")
	actions := []string{
		"on_discover", "on_search", "on_select", "on_init",
		"on_confirm", "on_status", "on_cancel", "on_update",
	}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			srv := newRecordingServer(t, http.StatusOK)
			w, _, _ := New(context.Background(), map[string]any{})
			rt := w.Wrap(http.DefaultTransport)

			body := []byte(`{"context":{"action":"` + action + `"},"message":{}}`)
			req, _ := http.NewRequest(http.MethodPost, srv.URL,
				bytes.NewReader(body))
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			drainAndClose(resp.Body)

			var env map[string]json.RawMessage
			_ = json.Unmarshal(srv.Last().Body, &env)
			var got string
			_ = json.Unmarshal(env["class_method"], &got)
			if got != action {
				t.Errorf("class_method = %q, want %q", got, action)
			}
		})
	}
}

func TestRoundTrip_RejectsNonCallbackAction(t *testing.T) {
	installFakeTokens(t, "tok")
	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, "http://example.com",
		bytes.NewReader(body))
	_, err := rt.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "does not match any allowed prefix") {
		t.Errorf("expected prefix-mismatch error, got %v", err)
	}
}

func TestRoundTrip_BadBodyReturnsError(t *testing.T) {
	installFakeTokens(t, "tok")
	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	cases := map[string][]byte{
		"empty body":         {},
		"not JSON":           []byte("not-json"),
		"missing context":    []byte(`{"message":{}}`),
		"context not object": []byte(`{"context":"x"}`),
		"action missing":     []byte(`{"context":{}}`),
		"action not string":  []byte(`{"context":{"action":42}}`),
		"action empty":       []byte(`{"context":{"action":""}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "http://example.com",
				bytes.NewReader(body))
			_, err := rt.RoundTrip(req)
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestRoundTrip_NilBodyReturnsError(t *testing.T) {
	installFakeTokens(t, "tok")
	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	// Request with no body at all (not even an empty Reader). extractAction
	// should report "body is empty" and the wrapper should surface it.
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", nil)
	_, err := rt.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "body is empty") {
		t.Errorf("expected 'body is empty' error, got %v", err)
	}
}

func TestRoundTrip_OriginalRequestNotMutated(t *testing.T) {
	installFakeTokens(t, "tok")
	srv := newRecordingServer(t, http.StatusOK)
	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("original request Authorization mutated, now %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "" {
		t.Errorf("original request Content-Type mutated, now %q", got)
	}
}

func TestRoundTrip_ImpersonationPathUsedWhenSAConfigured(t *testing.T) {
	installFakeTokens(t, "tok")

	srv := newRecordingServer(t, http.StatusOK)
	w, _, _ := New(context.Background(), map[string]any{
		"serviceAccount": "sa@p.iam.gserviceaccount.com",
	})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)

	// installFakeTokens stamps "tok#<sa>" only on the impersonation path.
	wantAuth := "Bearer tok#sa@p.iam.gserviceaccount.com"
	if got := srv.Last().Header.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want %q", got, wantAuth)
	}
}

// TestRoundTrip_RedirectReplaysWrappedBody verifies that a 307 redirect from
// the upstream replays the WRAPPED envelope, not the original Beckn body.
// This guards against http.Request.GetBody (shallow-copied by req.Clone)
// returning the unwrapped body.
func TestRoundTrip_RedirectReplaysWrappedBody(t *testing.T) {
	installFakeTokens(t, "tok")

	// Capture every body the upstream sees.
	var (
		mu             sync.Mutex
		bodiesReceived [][]byte
	)
	finalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodiesReceived = append(bodiesReceived, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"ack":{"status":"ACK"}}}`))
	}))
	defer finalSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodiesReceived = append(bodiesReceived, body)
		mu.Unlock()
		// 307 preserves method + body; the client must replay via GetBody.
		http.Redirect(w, r, finalSrv.URL+"/redirected", http.StatusTemporaryRedirect)
	}))
	defer redirectSrv.Close()

	w, _, _ := New(context.Background(), map[string]any{})
	// Use a dedicated client so we get redirect-following + access to our wrapper.
	client := &http.Client{Transport: w.Wrap(http.DefaultTransport)}

	originalBody := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, redirectSrv.URL,
		bytes.NewReader(originalBody))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	drainAndClose(resp.Body)

	mu.Lock()
	defer mu.Unlock()
	if len(bodiesReceived) != 2 {
		t.Fatalf("upstream saw %d requests, want 2 (initial + redirect replay)",
			len(bodiesReceived))
	}
	for i, b := range bodiesReceived {
		if bytes.Equal(b, originalBody) {
			t.Errorf("hop %d received the ORIGINAL unwrapped body; "+
				"GetBody was not refreshed after setBody. body=%s", i, b)
		}
		// Sanity: each body must be the wrapped envelope.
		var env agentEngineEnvelope
		if err := json.Unmarshal(b, &env); err != nil {
			t.Errorf("hop %d body is not JSON: %v (body=%s)", i, err, b)
			continue
		}
		if env.ClassMethod != "on_discover" {
			t.Errorf("hop %d class_method = %q, want on_discover", i, env.ClassMethod)
		}
	}
}

// TestRoundTrip_RespectsRequestContext verifies that a hung token-mint call
// does not outlive the per-request context deadline.
func TestRoundTrip_RespectsRequestContext(t *testing.T) {
	// A token source whose Token() blocks until the test cleanup signals it.
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return blockingTokenSource{stop: stop}, nil
	}

	w, _, err := New(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rt := w.Wrap(http.DefaultTransport)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	body := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.com",
		bytes.NewReader(body))

	start := time.Now()
	_, err = rt.RoundTrip(req)
	elapsed := time.Since(start)

	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	// Should bail roughly at the deadline; allow plenty of headroom for slow CI.
	if elapsed > 2*time.Second {
		t.Errorf("RoundTrip blocked for %s, expected ~50ms", elapsed)
	}
}

type blockingTokenSource struct{ stop <-chan struct{} }

func (b blockingTokenSource) Token() (*oauth2.Token, error) {
	<-b.stop
	return &oauth2.Token{AccessToken: "never-returned"}, nil
}

// ---- buildTokenSource ----------------------------------------------------

func TestBuildTokenSource_FactoryError(t *testing.T) {
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return nil, errors.New("boom from credentials library")
	}

	_, err := buildTokenSource(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error containing 'boom', got %v", err)
	}
}

func TestBuildTokenSource_ImpersonationFactoryUsed(t *testing.T) {
	origDef := defaultOAuth2TokenSource
	origImp := impersonateOAuth2TokenSource
	defer func() {
		defaultOAuth2TokenSource = origDef
		impersonateOAuth2TokenSource = origImp
	}()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		t.Error("ADC path should not be used when service account is configured")
		return nil, nil
	}
	impersonateOAuth2TokenSource = func(ctx context.Context, sa string, scopes []string) (oauth2.TokenSource, error) {
		return &fakeTokenSource{token: "imp:" + sa}, nil
	}

	ts, err := buildTokenSource(context.Background(), "sa@p.iam.gserviceaccount.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tok, _ := ts.Token()
	if tok.AccessToken != "imp:sa@p.iam.gserviceaccount.com" {
		t.Errorf("unexpected token: %q", tok.AccessToken)
	}
}

// ---- allowedActionPrefixes / passthroughOther --------------------------

func TestNew_AllowedActionPrefixes(t *testing.T) {
	installFakeTokens(t, "tok")

	t.Run("absent defaults to on_", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := w.allowedActionPrefixes; len(got) != 1 || got[0] != "on_" {
			t.Errorf("default allowedActionPrefixes = %v, want [on_]", got)
		}
	})
	t.Run("explicit list parsed", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{
			"allowedActionPrefixes": []any{"on_", "search"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := w.allowedActionPrefixes; len(got) != 2 || got[0] != "on_" || got[1] != "search" {
			t.Errorf("allowedActionPrefixes = %v, want [on_ search]", got)
		}
	})
	t.Run("native []string accepted", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{
			"allowedActionPrefixes": []string{"on_"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if len(w.allowedActionPrefixes) != 1 {
			t.Errorf("allowedActionPrefixes len = %d, want 1", len(w.allowedActionPrefixes))
		}
	})
	t.Run("empty list rejected", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"allowedActionPrefixes": []any{},
		})
		if err == nil || !strings.Contains(err.Error(), "at least one prefix") {
			t.Errorf("expected 'at least one prefix' error, got %v", err)
		}
	})
	t.Run("wrong type rejected", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"allowedActionPrefixes": "on_",
		})
		if err == nil || !strings.Contains(err.Error(), "allowedActionPrefixes") {
			t.Errorf("expected error mentioning 'allowedActionPrefixes', got %v", err)
		}
	})
	t.Run("element wrong type rejected", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"allowedActionPrefixes": []any{"on_", 42},
		})
		if err == nil || !strings.Contains(err.Error(), "element 1") {
			t.Errorf("expected element-index error, got %v", err)
		}
	})
}

func TestNew_PassthroughOther(t *testing.T) {
	installFakeTokens(t, "tok")

	t.Run("absent defaults to false", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if w.passthroughOther {
			t.Error("default passthroughOther = true, want false")
		}
	})
	t.Run("true honored", func(t *testing.T) {
		w, _, err := New(context.Background(), map[string]any{
			"passthroughOther": true,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if !w.passthroughOther {
			t.Error("passthroughOther = false, want true")
		}
	})
	t.Run("wrong type rejected", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"passthroughOther": "true",
		})
		if err == nil || !strings.Contains(err.Error(), "passthroughOther") {
			t.Errorf("expected error mentioning 'passthroughOther', got %v", err)
		}
	})
}

func TestRoundTrip_CustomPrefixWrapped(t *testing.T) {
	installFakeTokens(t, "tok")

	srv := newRecordingServer(t, http.StatusOK)
	w, _, _ := New(context.Background(), map[string]any{
		"allowedActionPrefixes": []any{"search"},
	})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"search"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)

	last := srv.Last()
	if last == nil {
		t.Fatal("server received no request")
	}
	if got := last.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", got)
	}
	// The body should be wrapped into the Agent Engine envelope with
	// class_method = "search".
	var env agentEngineEnvelope
	if err := json.Unmarshal(last.Body, &env); err != nil {
		t.Fatalf("server body is not the wrapped envelope: %v (body=%s)", err, last.Body)
	}
	if env.ClassMethod != "search" {
		t.Errorf("class_method = %q, want search", env.ClassMethod)
	}
}

func TestRoundTrip_PassthroughForwardsUnmodified(t *testing.T) {
	installFakeTokens(t, "tok")

	srv := newRecordingServer(t, http.StatusOK)
	w, _, _ := New(context.Background(), map[string]any{
		"passthroughOther": true,
	})
	rt := w.Wrap(http.DefaultTransport)

	originalBody := []byte(`{"context":{"action":"discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL,
		bytes.NewReader(originalBody))
	// Caller sets their own Authorization header (e.g., Beckn signature).
	// Passthrough must preserve it.
	req.Header.Set("Authorization", "BecknSignature keyId=foo")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)

	last := srv.Last()
	if last == nil {
		t.Fatal("server received no request")
	}
	if !bytes.Equal(last.Body, originalBody) {
		t.Errorf("upstream body = %s, want original %s", last.Body, originalBody)
	}
	if got := last.Header.Get("Authorization"); got != "BecknSignature keyId=foo" {
		t.Errorf("Authorization = %q, want untouched 'BecknSignature keyId=foo'", got)
	}
	// Default Content-Type isn't forced in passthrough mode.
	if got := last.Header.Get("Content-Type"); got == "application/json" {
		// (httptest may infer it from the request, but our wrapper should not
		// have set it explicitly. This assertion is informational rather than
		// strict because Go's net/http machinery may auto-add headers.)
		t.Logf("note: Content-Type=%q was set somewhere in the chain", got)
	}
}

func TestRoundTrip_PassthroughPreservesBodyForRedirect(t *testing.T) {
	installFakeTokens(t, "tok")

	// Capture every body the upstream sees.
	var (
		mu             sync.Mutex
		bodiesReceived [][]byte
	)
	finalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodiesReceived = append(bodiesReceived, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer finalSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodiesReceived = append(bodiesReceived, body)
		mu.Unlock()
		http.Redirect(w, r, finalSrv.URL+"/redirected", http.StatusTemporaryRedirect)
	}))
	defer redirectSrv.Close()

	w, _, _ := New(context.Background(), map[string]any{
		"passthroughOther": true,
	})
	client := &http.Client{Transport: w.Wrap(http.DefaultTransport)}

	originalBody := []byte(`{"context":{"action":"discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, redirectSrv.URL,
		bytes.NewReader(originalBody))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	drainAndClose(resp.Body)

	mu.Lock()
	defer mu.Unlock()
	if len(bodiesReceived) != 2 {
		t.Fatalf("upstream saw %d requests, want 2 (initial + redirect replay)",
			len(bodiesReceived))
	}
	for i, b := range bodiesReceived {
		if !bytes.Equal(b, originalBody) {
			t.Errorf("hop %d received body=%s, want original=%s", i, b, originalBody)
		}
	}
}

func TestRoundTrip_PassthroughTokenSourceNotInvoked(t *testing.T) {
	// Even though New() builds a token source eagerly, the passthrough
	// path must NOT mint or attach a token. Use a token source that errors
	// loudly if Token() is called.
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return panickingTokenSource{}, nil
	}

	srv := newRecordingServer(t, http.StatusOK)
	w, _, err := New(context.Background(), map[string]any{
		"passthroughOther": true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	drainAndClose(resp.Body)
	// If we reached here without panicking, the token source was untouched.
}

type panickingTokenSource struct{}

func (panickingTokenSource) Token() (*oauth2.Token, error) {
	panic("Token() should not be called in passthrough mode")
}

// ---- extractAction / wrapEnvelope ----------------------------------------

func TestExtractAction(t *testing.T) {
	cases := map[string]struct {
		body    []byte
		want    string
		wantErr string
	}{
		"happy path":   {[]byte(`{"context":{"action":"on_discover"}}`), "on_discover", ""},
		"empty":        {nil, "", "body is empty"},
		"not JSON":     {[]byte(`x`), "", "not a valid JSON"},
		"no context":   {[]byte(`{}`), "", "missing top-level 'context'"},
		"ctx not obj":  {[]byte(`{"context":"x"}`), "", "'context' is not a JSON object"},
		"no action":    {[]byte(`{"context":{}}`), "", "'context.action' field is missing"},
		"action int":   {[]byte(`{"context":{"action":42}}`), "", "'context.action' is not a JSON string"},
		"action empty": {[]byte(`{"context":{"action":""}}`), "", "'context.action' is empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := extractAction(c.body)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("err = %v, want substring %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWrapEnvelope(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		body := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
		out, err := wrapEnvelope("on_discover", body)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		var env agentEngineEnvelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("output not JSON: %v", err)
		}
		if env.ClassMethod != "on_discover" {
			t.Errorf("class_method = %q", env.ClassMethod)
		}
		if !bytes.Equal(env.Input.Request, body) {
			t.Errorf("input.request not preserved verbatim")
		}
	})
	t.Run("empty action", func(t *testing.T) {
		_, err := wrapEnvelope("", []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "action is empty") {
			t.Errorf("expected 'action is empty' error, got %v", err)
		}
	})
	t.Run("invalid body JSON", func(t *testing.T) {
		_, err := wrapEnvelope("on_discover", []byte("not-json"))
		if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Errorf("expected 'not valid JSON' error, got %v", err)
		}
	})
}
