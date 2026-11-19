package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

// plainCache implements definition.Cache only. It stands in for a third-party
// cache plugin that predates definition.AtomicCache.
type plainCache struct {
	mu     sync.Mutex
	data   map[string]string
	getErr error
	setErr error
}

func newPlainCache() *plainCache { return &plainCache{data: map[string]string{}} }

func (c *plainCache) Get(_ context.Context, key string) (string, error) {
	if c.getErr != nil {
		return "", c.getErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data[key], nil
}

func (c *plainCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
	return nil
}

func (c *plainCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}

func (c *plainCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = map[string]string{}
	return nil
}

// atomicCache adds a real compare-and-set, standing in for the Redis cache.
type atomicCache struct {
	plainCache
	setNXErr error
	// ttls records the TTL each key was claimed with, so tests can assert the
	// claim is scoped to the signature's own lifetime.
	ttls map[string]time.Duration
}

func newAtomicCache() *atomicCache {
	return &atomicCache{
		plainCache: plainCache{data: map[string]string{}},
		ttls:       map[string]time.Duration{},
	}
}

func (c *atomicCache) SetNX(_ context.Context, key, value string, ttl time.Duration) (bool, error) {
	if c.setNXErr != nil {
		return false, c.setNXErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.data[key]; exists {
		return false, nil
	}
	c.data[key] = value
	c.ttls[key] = ttl
	return true, nil
}

// compile-time proof the doubles model the real capability split.
var (
	_ definition.Cache       = (*plainCache)(nil)
	_ definition.AtomicCache = (*atomicCache)(nil)
)

// liveAuthHeader builds a Signature header whose window is still open, so the
// guard actually claims it. The shared helpers in step_test.go use fixed
// timestamps from 2023, which the guard correctly skips as already expired.
func liveAuthHeader(subscriberID, signature string, validFor time.Duration) string {
	now := time.Now().Unix()
	return fmt.Sprintf(
		`Signature keyId="%s|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",headers="(created) (expires) digest",signature="%s"`,
		subscriberID, now, now+int64(validFor.Seconds()), signature,
	)
}

func newTestGuard(t *testing.T, cache definition.Cache, cfg *ReplayGuardConfig) *replayGuard {
	t.Helper()
	return newReplayGuard(context.Background(), cache, cfg)
}

func guardCtx() *model.StepContext {
	return &model.StepContext{Context: context.Background()}
}

// ---------------------------------------------------------------------------
// core behaviour
// ---------------------------------------------------------------------------

func TestReplayGuard_SecondArrivalOfSameSignatureIsRejected(t *testing.T) {
	g := newTestGuard(t, newAtomicCache(), nil)
	header := liveAuthHeader("bap.example.com", "sigAAA==", 5*time.Minute)

	if err := g.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("first arrival should be accepted, got: %v", err)
	}

	err := g.check(guardCtx(), "bap.example.com", header)
	if err == nil {
		t.Fatal("replayed signature was accepted a second time")
	}

	var coded *model.CodedErr
	if !errors.As(err, &coded) {
		t.Fatalf("expected a *model.CodedErr, got %T: %v", err, err)
	}
	if got := coded.BecknError().Code; got != codeReplayDetected {
		t.Errorf("code = %q, want %q", got, codeReplayDetected)
	}
}

func TestReplayGuard_ResignedRetryIsAccepted(t *testing.T) {
	// The whole point of keying on the signature rather than message_id: a
	// peer that re-signs its retry produces a different signature and must
	// still get through.
	g := newTestGuard(t, newAtomicCache(), nil)

	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sigFIRST==", 5*time.Minute)); err != nil {
		t.Fatalf("original request rejected: %v", err)
	}
	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sigSECOND==", 5*time.Minute)); err != nil {
		t.Fatalf("re-signed retry rejected: %v", err)
	}
}

func TestReplayGuard_SameSignatureFromDifferentSubscribersDoesNotCollide(t *testing.T) {
	g := newTestGuard(t, newAtomicCache(), nil)
	const sig = "sigSHARED=="

	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", sig, 5*time.Minute)); err != nil {
		t.Fatalf("first subscriber rejected: %v", err)
	}
	if err := g.check(guardCtx(), "bpp.example.com", liveAuthHeader("bpp.example.com", sig, 5*time.Minute)); err != nil {
		t.Fatalf("second subscriber wrongly treated as a replay: %v", err)
	}
}

func TestReplayGuard_ExpiredSignatureIsNotClaimed(t *testing.T) {
	// An already-expired signature cannot pass checkTimestampWindow, so
	// spending a cache entry on it would be pure waste.
	cache := newAtomicCache()
	g := newTestGuard(t, cache, nil)

	now := time.Now().Unix()
	header := fmt.Sprintf(
		`Signature keyId="bap.example.com|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d",signature="sigOLD=="`,
		now-600, now-60,
	)
	if err := g.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cache.data) != 0 {
		t.Errorf("expected no claim stored for an expired signature, got %d keys", len(cache.data))
	}
}

func TestReplayGuard_MissingSignatureIsIgnored(t *testing.T) {
	cache := newAtomicCache()
	g := newTestGuard(t, cache, nil)

	// No signature attribute: the validator rejects this on its own, and there
	// is nothing stable for the guard to key on.
	header := `Signature keyId="bap.example.com|key-1|ed25519",algorithm="ed25519",created="1",expires="2"`
	if err := g.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cache.data) != 0 {
		t.Errorf("expected no claim stored, got %d keys", len(cache.data))
	}
}

// ---------------------------------------------------------------------------
// TTL derivation
// ---------------------------------------------------------------------------

func TestReplayGuard_ClaimTTLTracksSignatureLifetime(t *testing.T) {
	cache := newAtomicCache()
	g := newTestGuard(t, cache, nil)

	const window = 2 * time.Minute
	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sigTTL==", window)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cache.ttls) != 1 {
		t.Fatalf("expected exactly one claim, got %d", len(cache.ttls))
	}
	for _, ttl := range cache.ttls {
		// The claim must outlive the signature (window + grace) but not by much:
		// a shorter TTL would leave a gap where a replay slips through.
		if ttl <= window {
			t.Errorf("ttl = %v, want greater than the signature window %v", ttl, window)
		}
		if ttl > window+replayTTLGrace+2*time.Second {
			t.Errorf("ttl = %v, want no more than window+grace (%v)", ttl, window+replayTTLGrace)
		}
	}
}

func TestReplayTTL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	header := func(expires int64) string {
		return fmt.Sprintf(`Signature algorithm="ed25519",created="1",expires="%d",signature="s=="`, expires)
	}

	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"live window gets window plus grace", header(now.Unix() + 60), 60*time.Second + replayTTLGrace},
		{"already expired gets no claim", header(now.Unix() - 1), 0},
		{"expiring exactly now gets no claim", header(now.Unix()), 0},
		{
			// Guards against a disabled validity bound turning every claim into
			// a near-permanent cache entry.
			"absurd expiry is capped at the ceiling",
			header(now.Unix() + int64((10 * 365 * 24 * time.Hour).Seconds())),
			replayTTLCeiling,
		},
		{"unparseable expires falls back to the ceiling", `Signature algorithm="ed25519",signature="s=="`, replayTTLCeiling},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replayTTL(tt.header, now); got != tt.want {
				t.Errorf("replayTTL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthHeaderExpiry(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   int64
		ok     bool
	}{
		{"standard header", `Signature keyId="a|b|ed25519",algorithm="ed25519",created="10",expires="20",signature="s=="`, 20, true},
		{"spaces around attributes", `Signature created="10", expires="20", signature="s=="`, 20, true},
		{"attribute order does not matter", `Signature expires="99",created="10",signature="s=="`, 99, true},
		{"absent", `Signature created="10",signature="s=="`, 0, false},
		{"non-numeric", `Signature expires="soon",signature="s=="`, 0, false},
		{
			// "(expires)" inside the headers list must not be mistaken for the
			// expires attribute itself.
			"headers list is not mistaken for the attribute",
			`Signature created="10",expires="20",headers="(created) (expires) digest",signature="s=="`,
			20, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := authHeaderExpiry(tt.header)
			if ok != tt.ok || got != tt.want {
				t.Errorf("authHeaderExpiry() = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// cache-failure policy
// ---------------------------------------------------------------------------

func TestReplayGuard_CacheErrorAllowsByDefault(t *testing.T) {
	cache := newAtomicCache()
	cache.setNXErr = errors.New("redis is down")
	g := newTestGuard(t, cache, nil)

	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sig==", time.Minute)); err != nil {
		t.Fatalf("default policy should allow on cache error, got: %v", err)
	}
}

func TestReplayGuard_CacheErrorDeniesWhenConfigured(t *testing.T) {
	cache := newAtomicCache()
	cache.setNXErr = errors.New("redis is down")
	g := newTestGuard(t, cache, &ReplayGuardConfig{OnCacheError: "deny"})

	err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sig==", time.Minute))
	if err == nil {
		t.Fatal("onCacheError=deny should fail the request")
	}
	// A cache outage is not an authentication failure, so it must not be
	// classified as one: the caller should not be told their signature was bad.
	var coded *model.CodedErr
	if errors.As(err, &coded) {
		t.Errorf("cache outage should stay unclassified, got coded error %q", coded.BecknError().Code)
	}
	if !strings.Contains(err.Error(), "redis is down") {
		t.Errorf("error should wrap the cache failure, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// construction
// ---------------------------------------------------------------------------

func TestNewReplayGuard(t *testing.T) {
	disabled := false
	enabled := true

	t.Run("nil config enables the guard", func(t *testing.T) {
		if newTestGuard(t, newAtomicCache(), nil) == nil {
			t.Error("guard should default to enabled")
		}
	})

	t.Run("explicitly enabled", func(t *testing.T) {
		if newTestGuard(t, newAtomicCache(), &ReplayGuardConfig{Enabled: &enabled}) == nil {
			t.Error("guard should be built when enabled")
		}
	})

	t.Run("explicitly disabled", func(t *testing.T) {
		if newTestGuard(t, newAtomicCache(), &ReplayGuardConfig{Enabled: &disabled}) != nil {
			t.Error("guard should be nil when disabled")
		}
	})

	t.Run("no cache means no guard", func(t *testing.T) {
		if newTestGuard(t, nil, nil) != nil {
			t.Error("guard should be nil without a cache")
		}
	})

	t.Run("atomic cache is preferred", func(t *testing.T) {
		g := newTestGuard(t, newAtomicCache(), nil)
		if g.atomic == nil {
			t.Error("expected the atomic path to be selected")
		}
	})

	t.Run("plain cache degrades to the fallback path", func(t *testing.T) {
		g := newTestGuard(t, newPlainCache(), nil)
		if g.atomic != nil {
			t.Error("plain cache must not be used through the atomic path")
		}
		if g.fallback == nil {
			t.Error("expected the fallback path to be wired")
		}
	})
}

func TestReplayGuard_NilGuardIsANoOp(t *testing.T) {
	var g *replayGuard
	if err := g.check(guardCtx(), "bap.example.com", liveAuthHeader("bap.example.com", "sig==", time.Minute)); err != nil {
		t.Errorf("a nil guard must accept everything, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// fallback path
// ---------------------------------------------------------------------------

func TestReplayGuard_FallbackPathStillCatchesSequentialReplays(t *testing.T) {
	// A cache without SetNX cannot catch two simultaneous replays, but the
	// ordinary sequential case must still be rejected.
	g := newTestGuard(t, newPlainCache(), nil)
	header := liveAuthHeader("bap.example.com", "sigSEQ==", time.Minute)

	if err := g.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("first arrival rejected: %v", err)
	}
	if err := g.check(guardCtx(), "bap.example.com", header); err == nil {
		t.Error("fallback path failed to catch a sequential replay")
	}
}

func TestReplayGuard_FallbackPathCacheErrors(t *testing.T) {
	t.Run("get error", func(t *testing.T) {
		c := newPlainCache()
		c.getErr = errors.New("get failed")
		g := newTestGuard(t, c, &ReplayGuardConfig{OnCacheError: "deny"})
		if err := g.check(guardCtx(), "s", liveAuthHeader("s", "sig==", time.Minute)); err == nil {
			t.Error("expected the get failure to surface under onCacheError=deny")
		}
	})

	t.Run("set error", func(t *testing.T) {
		c := newPlainCache()
		c.setErr = errors.New("set failed")
		g := newTestGuard(t, c, &ReplayGuardConfig{OnCacheError: "deny"})
		if err := g.check(guardCtx(), "s", liveAuthHeader("s", "sig==", time.Minute)); err == nil {
			t.Error("expected the set failure to surface under onCacheError=deny")
		}
	})
}

// ---------------------------------------------------------------------------
// concurrency
// ---------------------------------------------------------------------------

func TestReplayGuard_ConcurrentReplaysClaimExactlyOnce(t *testing.T) {
	// The reason definition.AtomicCache exists: N goroutines replaying one
	// signature together must yield exactly one acceptance.
	g := newTestGuard(t, newAtomicCache(), nil)
	header := liveAuthHeader("bap.example.com", "sigRACE==", 5*time.Minute)

	const n = 32
	var wg sync.WaitGroup
	results := make([]error, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = g.check(guardCtx(), "bap.example.com", header)
		}(i)
	}
	close(start)
	wg.Wait()

	accepted := 0
	for _, err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("accepted %d of %d concurrent replays, want exactly 1", accepted, n)
	}
}

// ---------------------------------------------------------------------------
// key construction
// ---------------------------------------------------------------------------

func TestReplayKey(t *testing.T) {
	k := replayKey("onix", "bap.example.com", "sigAAA==")

	if !strings.HasPrefix(k, "replay:onix:sig:") {
		t.Errorf("key = %q, want the replay:onix:sig: prefix", k)
	}
	// The peer-supplied signature is key material and must not be readable in
	// Redis keyspace dumps, slow-query logs or monitoring output.
	if strings.Contains(k, "sigAAA==") {
		t.Error("key must not embed the raw signature")
	}
	if k == replayKey("onix", "bpp.example.com", "sigAAA==") {
		t.Error("different subscribers must produce different keys")
	}
	if k == replayKey("other", "bap.example.com", "sigAAA==") {
		t.Error("different namespaces must produce different keys")
	}
	if k != replayKey("onix", "bap.example.com", "sigAAA==") {
		t.Error("key construction must be deterministic")
	}
}

func TestReplayGuard_NamespaceIsolatesDeployments(t *testing.T) {
	shared := newAtomicCache()
	a := newTestGuard(t, shared, &ReplayGuardConfig{Namespace: "deployment-a"})
	b := newTestGuard(t, shared, &ReplayGuardConfig{Namespace: "deployment-b"})

	header := liveAuthHeader("bap.example.com", "sigNS==", time.Minute)
	if err := a.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("deployment-a rejected: %v", err)
	}
	if err := b.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Errorf("deployment-b should not see deployment-a's claim: %v", err)
	}
}

// ---------------------------------------------------------------------------
// integration through validateSignStep
// ---------------------------------------------------------------------------

func makeReplayStepCtx(authHeader string) *model.StepContext {
	body := `{"context":{"action":"search","messageId":"msg-replay-001","version":"2.0.0"}}`
	req, _ := http.NewRequest(http.MethodPost, "/bap/receiver/search", strings.NewReader(body))
	req.Header.Set(model.AuthHeaderSubscriber, authHeader)
	return &model.StepContext{
		Context:         context.Background(),
		Request:         req,
		Body:            []byte(body),
		ProtocolVersion: "2.0.0",
		SubID:           "bap.example.com",
		MessageID:       "msg-replay-001",
		RespHeader:      http.Header{},
	}
}

func TestValidateSignStep_RejectsReplayedRequest(t *testing.T) {
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	guard := newTestGuard(t, newAtomicCache(), nil)

	step, err := newValidateSignStep(sv, km, nil, guard)
	if err != nil {
		t.Fatalf("newValidateSignStep() error: %v", err)
	}
	vStep := step.(*validateSignStep)

	header := liveAuthHeader("bap.example.com", "sigStep==", 5*time.Minute)

	if err := vStep.validateHeaders(makeReplayStepCtx(header)); err != nil {
		t.Fatalf("original request rejected: %v", err)
	}

	ctx := makeReplayStepCtx(header)
	err = vStep.validateHeaders(ctx)
	if err == nil {
		t.Fatal("replayed request was accepted by the step")
	}

	var coded *model.CodedErr
	if !errors.As(err, &coded) {
		t.Fatalf("expected *model.CodedErr, got %T: %v", err, err)
	}
	if got := coded.BecknError().Code; got != codeReplayDetected {
		t.Errorf("code = %q, want %q", got, codeReplayDetected)
	}
	if got := coded.HTTPStatus(); got != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", got, http.StatusUnauthorized)
	}
	if ctx.RespHeader.Get(model.UnaAuthorizedHeaderSubscriber) == "" {
		t.Error("expected the WWW-Authenticate style challenge header to be set on a replay rejection")
	}
}

func TestValidateSignStep_ClaimsOnlyAfterSignatureVerifies(t *testing.T) {
	// A forged signature must not consume a claim, otherwise an attacker could
	// pre-poison the cache with keys the real peer would later need.
	cache := newAtomicCache()
	sv := &mockSignValidatorBasic{validateErr: errors.New("bad signature")}
	km := &mockKMBasic{publicKey: "pubKey=="}
	guard := newTestGuard(t, cache, nil)

	step, _ := newValidateSignStep(sv, km, nil, guard)
	vStep := step.(*validateSignStep)

	header := liveAuthHeader("bap.example.com", "forged==", 5*time.Minute)
	if err := vStep.validateHeaders(makeReplayStepCtx(header)); err == nil {
		t.Fatal("expected the forged signature to be rejected")
	}
	if len(cache.data) != 0 {
		t.Errorf("a failed verification must not claim a key, got %d keys", len(cache.data))
	}
}

func TestValidateSignStep_NilGuardAcceptsRepeats(t *testing.T) {
	// Documents the pre-fix behaviour that the guard exists to change.
	sv := &mockSignValidatorBasic{}
	km := &mockKMBasic{publicKey: "pubKey=="}
	step, _ := newValidateSignStep(sv, km, nil, nil)
	vStep := step.(*validateSignStep)

	header := liveAuthHeader("bap.example.com", "sigNoGuard==", 5*time.Minute)
	for i := 0; i < 3; i++ {
		if err := vStep.validateHeaders(makeReplayStepCtx(header)); err != nil {
			t.Fatalf("attempt %d rejected without a guard: %v", i+1, err)
		}
	}
}

func TestReplayGuard_SignatureFirstInHeaderIsStillClaimed(t *testing.T) {
	// Regression: a substring scan anchored on ",signature=\"" misses an
	// attribute a peer put first in the header. A signature the guard cannot
	// see is a signature it cannot claim, which is a silent bypass.
	g := newTestGuard(t, newAtomicCache(), nil)

	now := time.Now().Unix()
	header := fmt.Sprintf(
		`Signature signature="sigFIRST==",keyId="bap.example.com|key-1|ed25519",algorithm="ed25519",created="%d",expires="%d"`,
		now, now+300,
	)

	if err := g.check(guardCtx(), "bap.example.com", header); err != nil {
		t.Fatalf("first arrival rejected: %v", err)
	}
	if err := g.check(guardCtx(), "bap.example.com", header); err == nil {
		t.Error("replay bypassed the guard when signature was the first header attribute")
	}
}

func TestAuthHeaderAttr_DoesNotConfuseRequestSignature(t *testing.T) {
	// "request-signature" must never be read as "signature": claiming the
	// wrong value would key the claim on a field the peer can vary freely.
	header := `Signature keyId="a|b|ed25519",request-signature="OTHER==",signature="REAL==",created="1",expires="2"`

	got, ok := authHeaderAttr(header, "signature")
	if !ok || got != "REAL==" {
		t.Errorf("authHeaderAttr(signature) = (%q, %v), want (\"REAL==\", true)", got, ok)
	}
	if got, ok := authHeaderAttr(header, "request-signature"); !ok || got != "OTHER==" {
		t.Errorf("authHeaderAttr(request-signature) = (%q, %v), want (\"OTHER==\", true)", got, ok)
	}
	if _, ok := authHeaderAttr(header, "nosuchattr"); ok {
		t.Error("absent attribute reported as present")
	}
}
