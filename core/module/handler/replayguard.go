package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

// codeReplayDetected is the AUT_* taxonomy value for a signature that has
// already been accepted once. It was listed in #864's scope but had no
// corresponding check until this guard existed, so signvalidator's own comment
// records it as deliberately unreachable.
const codeReplayDetected = "AUT_REPLAY_DETECTED"

const (
	// replayNamespaceDefault scopes replay keys so two ONIX deployments sharing
	// one Redis do not see each other's claims.
	replayNamespaceDefault = "onix"

	// replayTTLGrace extends a claim slightly past the signature's own expiry.
	// Without it a claim could lapse a fraction before the last instant at
	// which checkTimestampWindow still accepts the signature, leaving a narrow
	// window where a replay would pass.
	replayTTLGrace = 10 * time.Second

	// replayTTLCeiling caps how long a single claim is held, independently of
	// what the signature's own expires says. signvalidator bounds the validity
	// window too, but an operator can disable that bound, and this guard must
	// not become a memory-exhaustion vector when they do.
	replayTTLCeiling = time.Hour
)

// ReplayGuardConfig configures inbound signature replay rejection.
//
// The guard remembers every signature it accepts, for as long as that
// signature could still be accepted again, and rejects the second and later
// arrivals of the same one.
type ReplayGuardConfig struct {
	// Enabled turns the guard on. nil means enabled: a security control that
	// ships off protects nobody. Set it to false to restore the previous
	// behaviour of accepting an unlimited number of copies of one signature.
	Enabled *bool `yaml:"enabled,omitempty"`

	// OnCacheError decides what happens when the backing cache cannot answer.
	// "allow" (the default) logs and lets the request through, matching how
	// payloadstore and manifestloader already treat cache faults, so a Redis
	// blip degrades security rather than dropping all traffic. "deny" fails
	// the request instead, for operators who would rather stop than accept
	// anything unverified.
	OnCacheError string `yaml:"onCacheError,omitempty"`

	// Namespace prefixes every replay key. Defaults to "onix".
	Namespace string `yaml:"namespace,omitempty"`
}

// enabled reports whether the guard should run. A nil config means "default
// settings", not "off", so an operator gets replay protection without having
// to discover a new YAML key.
func (c *ReplayGuardConfig) enabled() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// replayGuard claims an inbound signature exactly once.
type replayGuard struct {
	// atomic is the preferred path. It is nil when the configured Cache does
	// not implement definition.AtomicCache.
	atomic definition.AtomicCache
	// fallback is used only when atomic is nil.
	fallback  definition.Cache
	namespace string
	// failClosed reflects OnCacheError == "deny".
	failClosed bool
	metrics    *HandlerMetrics
}

// newReplayGuard builds a guard over the handler's cache. It returns nil when
// the guard is disabled or when no cache is configured, and callers treat a
// nil guard as "no replay checking", so the caller needs no extra branching.
func newReplayGuard(ctx context.Context, cache definition.Cache, cfg *ReplayGuardConfig) *replayGuard {
	if !cfg.enabled() {
		log.Warnf(ctx, "replay guard disabled by config: a verified signature can be accepted repeatedly until it expires")
		return nil
	}
	if cache == nil {
		// Not an error: plenty of modules (the Caller handlers) never run
		// validateSign at all, and a Receiver without a cache is a deployment
		// choice we surface rather than refuse.
		log.Warnf(ctx, "replay guard inactive: no Cache plugin configured for this module")
		return nil
	}

	namespace := replayNamespaceDefault
	failClosed := false
	if cfg != nil {
		if cfg.Namespace != "" {
			namespace = cfg.Namespace
		}
		failClosed = strings.EqualFold(cfg.OnCacheError, "deny")
	}

	g := &replayGuard{fallback: cache, namespace: namespace, failClosed: failClosed}
	if ac, ok := cache.(definition.AtomicCache); ok {
		g.atomic = ac
	} else {
		// Get-then-Set cannot tell the first claimant from a concurrent one:
		// two replays arriving together both observe a miss and both proceed.
		// Sequential replays are still caught, which is the common case, so
		// this degrades rather than fails.
		log.Warnf(ctx, "replay guard: configured Cache does not implement definition.AtomicCache; falling back to a non-atomic check that can miss simultaneous replays")
	}
	g.metrics, _ = GetHandlerMetrics(ctx)
	return g
}

// check claims the signature carried by authHeader on behalf of subscriberID.
//
// It must run only after that signature has been cryptographically verified.
// Claiming earlier would let unauthenticated traffic fill the cache with
// arbitrary keys. This is a deliberate departure from the dedup gate drawn in
// the PayloadStore design (#707), which sits ahead of validateSign.
//
// Returns a 401 AUT_REPLAY_DETECTED when this signature has been seen before.
func (g *replayGuard) check(ctx *model.StepContext, subscriberID, authHeader string) error {
	if g == nil {
		return nil
	}

	signature, _ := authHeaderAttr(authHeader, "signature")
	if signature == "" {
		// Nothing stable to key on. A missing signature is already rejected by
		// the validator itself, so there is no failure mode to report here.
		return nil
	}

	ttl := replayTTL(authHeader, time.Now())
	if ttl <= 0 {
		// The signature is at or past its own expiry, so it cannot be replayed
		// through the timestamp check anyway. Storing a claim would be dead weight.
		return nil
	}

	key := replayKey(g.namespace, subscriberID, signature)
	claimed, err := g.claim(ctx.Context, key, ttl)
	if err != nil {
		g.record(ctx.Context, "error")
		if g.failClosed {
			// Unclassified on purpose: this is not an authentication failure,
			// it is ONIX being unable to tell. It surfaces as a 500 rather than
			// a 401 so the caller does not read it as "your signature was bad".
			return fmt.Errorf("replay guard: cache unavailable and onCacheError=deny: %w", err)
		}
		log.Warnf(ctx, "replay guard: cache error, allowing request unchecked (onCacheError=allow): %v", err)
		return nil
	}

	if !claimed {
		g.record(ctx.Context, "replay")
		log.Warnf(ctx, "replay guard: rejecting a signature already accepted for subscriber %s", subscriberID)
		return model.NewSignValidationErr(codeReplayDetected, fmt.Errorf("signature has already been accepted; replayed requests are rejected until the signature expires"))
	}

	g.record(ctx.Context, "accepted")
	return nil
}

// claim stores the key if absent and reports whether this call stored it.
func (g *replayGuard) claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if g.atomic != nil {
		return g.atomic.SetNX(ctx, key, "1", ttl)
	}

	// Non-atomic fallback. definition.Cache.Get returns ("", nil) on a miss,
	// so a non-empty value is the only "already claimed" signal.
	existing, err := g.fallback.Get(ctx, key)
	if err != nil {
		return false, err
	}
	if existing != "" {
		return false, nil
	}
	if err := g.fallback.Set(ctx, key, "1", ttl); err != nil {
		return false, err
	}
	return true, nil
}

func (g *replayGuard) record(ctx context.Context, outcome string) {
	if g.metrics == nil || g.metrics.ReplayChecksTotal == nil {
		return
	}
	g.metrics.ReplayChecksTotal.Add(ctx, 1,
		metric.WithAttributes(telemetry.AttrStatus.String(outcome)))
}

// replayKey builds the cache key for one signature.
//
// The signature value is hashed rather than embedded: it is key material a
// peer supplied, and Redis keys turn up in logs, slow-query output and
// monitoring tools. The subscriber ID is part of the hashed input so two
// subscribers can never collide on one claim.
func replayKey(namespace, subscriberID, signature string) string {
	sum := sha256.Sum256([]byte(subscriberID + "|" + signature))
	return "replay:" + namespace + ":sig:" + hex.EncodeToString(sum[:])
}

// replayTTL returns how long a claim on this signature must be held: until the
// signature stops being accepted on its own, plus a small grace.
//
// A claim only has to outlive the window in which the signature would still
// pass checkTimestampWindow. Deriving it from expires rather than using a
// fixed TTL means there is no interval where a signature is acceptable but its
// claim has already lapsed, and no claim outlives its usefulness.
func replayTTL(authHeader string, now time.Time) time.Duration {
	expires, ok := authHeaderExpiry(authHeader)
	if !ok {
		// Unparseable expires. The validator rejects these, but if that ever
		// changes, hold the claim for the ceiling rather than skipping it.
		return replayTTLCeiling
	}

	ttl := time.Unix(expires, 0).Sub(now)
	if ttl <= 0 {
		return 0
	}
	ttl += replayTTLGrace
	if ttl > replayTTLCeiling {
		return replayTTLCeiling
	}
	return ttl
}

// authHeaderAttr returns the value of one attribute of a Beckn Authorization
// header, and whether it was present.
//
// It parses attributes the way signvalidator does and matches the key exactly,
// rather than scanning for a substring. That matters in both directions:
// attribute order and spacing do not affect the result, and "request-signature"
// is never mistaken for "signature". A substring scan anchored on a leading
// comma, as extractAuthSignature does, would miss an attribute a peer chose to
// put first in the header, and a signature the guard cannot see is a signature
// it cannot claim.
func authHeaderAttr(header, name string) (string, bool) {
	for _, part := range strings.Split(strings.TrimPrefix(header, "Signature "), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) != name {
			continue
		}
		return strings.Trim(kv[1], `"`), true
	}
	return "", false
}

// authHeaderExpiry pulls the expires attribute out of a Beckn Authorization header.
func authHeaderExpiry(header string) (int64, bool) {
	raw, ok := authHeaderAttr(header, "expires")
	if !ok {
		return 0, false
	}
	expires, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return expires, true
}
