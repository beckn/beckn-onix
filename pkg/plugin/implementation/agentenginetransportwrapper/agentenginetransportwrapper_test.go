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
// can distinguish which path was taken.
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
	mu     atomic.Value // holds *recordedRequest
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
		rs.mu.Store(&recordedRequest{
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
	v := rs.mu.Load()
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
	_, _, err := New(nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Errorf("expected nil-context error, got %v", err)
	}
}

func TestNew_RejectsWrongTypedConfigValues(t *testing.T) {
	t.Run("service_account wrong type", func(t *testing.T) {
		_, _, err := New(context.Background(), map[string]any{
			"service_account": []any{"x"}, // not a string
		})
		if err == nil || !strings.Contains(err.Error(), "service_account") {
			t.Errorf("expected error mentioning 'service_account', got %v", err)
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

func TestNew_AcceptsServiceAccount(t *testing.T) {
	w, closer, err := New(context.Background(), map[string]any{
		"service_account": "sa@p.iam.gserviceaccount.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if closer != nil {
		closer()
	}
	if w.serviceAccount != "sa@p.iam.gserviceaccount.com" {
		t.Errorf("service_account not parsed; got %q", w.serviceAccount)
	}
}

// ---- Wrap() --------------------------------------------------------------

func TestWrap_WithNilBaseUsesDefault(t *testing.T) {
	w := &Wrapper{ctx: context.Background()}
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
	if err == nil || !strings.Contains(err.Error(), "not an on_* callback") {
		t.Errorf("expected non-callback error, got %v", err)
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
		"service_account": "sa@p.iam.gserviceaccount.com",
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

func TestRoundTrip_TokenMintErrorPropagates(t *testing.T) {
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return nil, errors.New("boom from credentials lib")
	}

	w, _, _ := New(context.Background(), map[string]any{})
	rt := w.Wrap(http.DefaultTransport)

	body := []byte(`{"context":{"action":"on_discover"},"message":{}}`)
	req, _ := http.NewRequest(http.MethodPost, "http://example.com",
		bytes.NewReader(body))
	_, err := rt.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped 'boom' error, got %v", err)
	}
}

// ---- tokenSource() cache + concurrency -----------------------------------

func TestTokenSource_CachesSingleSource(t *testing.T) {
	var calls int32
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		atomic.AddInt32(&calls, 1)
		return &fakeTokenSource{token: "t"}, nil
	}

	tr := &aeTransport{ctx: context.Background()}
	for i := 0; i < 5; i++ {
		if _, err := tr.tokenSource(); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 underlying construction, got %d", calls)
	}
}

func TestTokenSource_ConcurrentAccess(t *testing.T) {
	var calls int32
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		atomic.AddInt32(&calls, 1)
		return &fakeTokenSource{token: "t"}, nil
	}

	tr := &aeTransport{ctx: context.Background()}
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = tr.tokenSource()
		}()
	}
	wg.Wait()

	// Allow 1-2 to tolerate the narrow race window; guards against gross over-construction.
	if calls > 2 {
		t.Errorf("expected at most 2 constructions under concurrency, got %d", calls)
	}
}

func TestTokenSource_FactoryError(t *testing.T) {
	origDef := defaultOAuth2TokenSource
	defer func() { defaultOAuth2TokenSource = origDef }()
	defaultOAuth2TokenSource = func(ctx context.Context, scopes ...string) (oauth2.TokenSource, error) {
		return nil, errors.New("boom from credentials library")
	}

	tr := &aeTransport{ctx: context.Background()}
	_, err := tr.tokenSource()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected error containing 'boom', got %v", err)
	}
}

func TestTokenSource_ImpersonationFactoryUsed(t *testing.T) {
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

	tr := &aeTransport{
		ctx:            context.Background(),
		serviceAccount: "sa@p.iam.gserviceaccount.com",
	}
	ts, err := tr.tokenSource()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tok, _ := ts.Token()
	if tok.AccessToken != "imp:sa@p.iam.gserviceaccount.com" {
		t.Errorf("unexpected token: %q", tok.AccessToken)
	}
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
