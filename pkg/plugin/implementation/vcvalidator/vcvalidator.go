// Package vcvalidator provides a processing Step that validates W3C
// Verifiable Credentials embedded in a beckn request body. It is built as
// validateVC.so, so pipelines reference it by the step id validateVC —
// matching the verb naming of the built-in steps (validateSign,
// validateSchema).
//
// For the configured beckn actions it verifies every embedded credential's
// proof, validity window and revocation status. On any failure the step
// returns an error, which the handler pipeline turns into the standard
// signed beckn NACK — the request never reaches routing.
//
// The package is organised in two files:
//
//   - vcvalidator.go — the plugin surface: the Step, its Config, and
//     credential extraction from the request body.
//   - verify.go — the verification engine: proof/JWT checks, DID
//     resolution (did:key / did:jwk / did:web), and revocation.
package vcvalidator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// ---------------------------------------------------------------------------
// Step
// ---------------------------------------------------------------------------

// step validates embedded Verifiable Credentials as part of the module's
// processing pipeline. It implements definition.Step.
type step struct {
	cfg *Config
	v   *verifier
}

// New builds the validateVC Step from its YAML config map.
func New(cfg map[string]string) (definition.Step, error) {
	config, err := ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("validateVC: config: %w", err)
	}

	client := newHTTPClient(config)
	v := newVerifier(config, httpFetcher(client))
	v.statusGet = httpStatusFetcher(client)

	return &step{cfg: config, v: v}, nil
}

// Run verifies every credential embedded in the request body. Requests for
// non-gated actions, or without embedded credentials, pass through untouched.
// A verification failure is returned as an error so the handler rejects the
// request through its standard signed-NACK path.
func (s *step) Run(ctx *model.StepContext) error {
	if !s.cfg.Enabled {
		return nil
	}

	action := extractAction(ctx.Request.URL.Path, ctx.Body)
	if !s.cfg.IsActionEnabled(action) {
		return nil
	}

	creds := extractCredentials(ctx.Body)
	if len(creds) == 0 {
		if s.cfg.DebugLogging {
			log.Debugf(ctx, "validateVC: action=%s: no embedded credentials, passing through", action)
		}
		return nil
	}

	// Cap the per-request workload before any network I/O: each credential can
	// cost up to httpTimeout for did:web resolution plus another for the
	// revocation fetch, so an unbounded count would let a single request tie up
	// a handler goroutine indefinitely.
	if len(creds) > s.cfg.MaxCredentials {
		ve := failf(failStructure, "request carries %d credentials, exceeding maxCredentials=%d",
			len(creds), s.cfg.MaxCredentials)
		log.Errorf(ctx, ve, "validateVC: action=%s rejected", action)
		return nackErr(ve)
	}

	for i, raw := range creds {
		if err := s.v.verify(ctx, raw); err != nil {
			ve := asVCError(err)
			log.Errorf(ctx, ve, "validateVC: action=%s credential[%d] rejected", action, i)
			return nackErr(ve)
		}
	}

	log.Infof(ctx, "validateVC: action=%s: %d credential(s) verified OK", action, len(creds))
	return nil
}

// nackErr wraps a credential failure in the model error type the handler's
// NACK mapping understands: a structurally broken credential is a Bad
// Request, while every other failure (proof, issuer, expiry, revocation,
// resolution) means the credential's authenticity could not be established,
// which maps to Unauthorized. The machine-readable failure class stays at the
// start of the NACK error message (e.g. "CREDENTIAL_REVOKED: …").
func nackErr(ve *vcError) error {
	if ve.class == failStructure {
		return model.NewBadReqErr(ve)
	}
	return model.NewSignValidationErr(ve)
}

func asVCError(err error) *vcError {
	if ve, ok := err.(*vcError); ok {
		return ve
	}
	return &vcError{class: failStructure, msg: err.Error()}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config holds configuration for the VC Validator plugin.
//
// The plugin inspects Verifiable Credentials carried in the request body
// (by default the credential objects nested under
// message.contract.participants[].participantAttributes) and, for the
// configured beckn actions, verifies that each credential:
//
//   - has a cryptographically valid proof (did:key / did:jwk / did:web),
//   - was signed by the did:web issuer when the issuer id is a did:web that
//     is web accessible,
//   - is within its validity window (validFrom / validUntil, nbf / exp), and
//   - is not revoked (credentialStatus).
//
// On any failure the request is rejected with a beckn NACK and never reaches
// routing.
type Config struct {
	// Enabled controls whether the plugin is active. When false the step
	// passes every request through untouched.
	Enabled bool

	// Actions is the list of beckn actions whose payloads are validated.
	// REQUIRED — no code default. Declared explicitly in the devkit YAML so
	// the operator can see exactly which messages are gated (e.g.
	// "confirm,init,select").
	Actions []string

	// AllowedDIDMethods restricts which issuer/verification-method DID
	// methods are accepted. Default: key,jwk,web.
	AllowedDIDMethods []string

	// CheckExpiry toggles validity-window enforcement (validFrom/validUntil
	// and JWT nbf/exp). Default: true.
	CheckExpiry bool

	// CheckRevocation toggles credentialStatus revocation checks.
	// Default: true.
	CheckRevocation bool

	// RequireProof rejects credentials whose proof cannot be cryptographically
	// verified by this plugin (e.g. JSON-LD Data Integrity proofs such as
	// Ed25519Signature2020 that require RDF canonicalization, which this
	// plugin does not perform). When false such proofs are skipped with a
	// warning and the remaining checks (expiry/revocation) still run.
	// Default: true.
	RequireProof bool

	// FailOpen controls behaviour on transient network errors while
	// resolving a did:web document or fetching a revocation list. When true
	// such errors are logged and the credential is allowed through; when
	// false the request is rejected. Default: false (fail closed).
	FailOpen bool

	// HTTPTimeout bounds did:web and revocation-list HTTP fetches.
	// Default: 10s.
	HTTPTimeout time.Duration

	// MaxCredentials caps how many embedded credentials a single request may
	// carry. Each credential can cost up to two HTTP fetches (did:web
	// resolution + revocation), so the cap bounds the per-request work; a
	// request exceeding it is rejected with a Bad Request NACK before any
	// network I/O. Default: 10.
	MaxCredentials int

	// AllowPrivateNetworks permits did:web and revocation fetches to resolve
	// to private, loopback or link-local addresses. The fetched URLs come from
	// the request body, so this MUST stay false in production (SSRF); it
	// exists for local/devkit deployments where issuers and registries live on
	// a private docker network. Default: false.
	AllowPrivateNetworks bool

	// DebugLogging enables verbose per-credential logging.
	DebugLogging bool
}

// DefaultConfig returns a Config seeded with sensible defaults for the
// non-primary fields. The primary behaviour knob (Actions) is intentionally
// left empty — ParseConfig requires it in the YAML.
func DefaultConfig() *Config {
	return &Config{
		Enabled:              true,
		AllowedDIDMethods:    []string{"key", "jwk", "web"},
		CheckExpiry:          true,
		CheckRevocation:      true,
		RequireProof:         true,
		FailOpen:             false,
		HTTPTimeout:          10 * time.Second,
		MaxCredentials:       10,
		AllowPrivateNetworks: false,
		DebugLogging:         false,
	}
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return def
	}
}

func splitCSV(v string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseConfig parses the plugin configuration map supplied by beckn-onix.
func ParseConfig(cfg map[string]string) (*Config, error) {
	config := DefaultConfig()

	if v, ok := cfg["enabled"]; ok {
		config.Enabled = parseBool(v, config.Enabled)
	}

	if v, ok := cfg["actions"]; ok {
		config.Actions = splitCSV(v)
	}

	if v, ok := cfg["allowedDidMethods"]; ok && strings.TrimSpace(v) != "" {
		// normalise: strip a leading "did:" if present.
		methods := splitCSV(v)
		for i := range methods {
			methods[i] = strings.TrimPrefix(strings.ToLower(methods[i]), "did:")
		}
		config.AllowedDIDMethods = methods
	}

	if v, ok := cfg["checkExpiry"]; ok {
		config.CheckExpiry = parseBool(v, config.CheckExpiry)
	}

	if v, ok := cfg["checkRevocation"]; ok {
		config.CheckRevocation = parseBool(v, config.CheckRevocation)
	}

	if v, ok := cfg["requireProof"]; ok {
		config.RequireProof = parseBool(v, config.RequireProof)
	}

	if v, ok := cfg["failOpen"]; ok {
		config.FailOpen = parseBool(v, config.FailOpen)
	}

	if v, ok := cfg["httpTimeout"]; ok && strings.TrimSpace(v) != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			config.HTTPTimeout = time.Duration(secs) * time.Second
		} else if d, err2 := time.ParseDuration(strings.TrimSpace(v)); err2 == nil {
			config.HTTPTimeout = d
		} else {
			return nil, fmt.Errorf("validateVC: invalid httpTimeout %q", v)
		}
	}

	if v, ok := cfg["maxCredentials"]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("validateVC: invalid maxCredentials %q (must be a positive integer)", v)
		}
		config.MaxCredentials = n
	}

	if v, ok := cfg["allowPrivateNetworks"]; ok {
		config.AllowPrivateNetworks = parseBool(v, config.AllowPrivateNetworks)
	}

	if v, ok := cfg["debugLogging"]; ok {
		config.DebugLogging = parseBool(v, config.DebugLogging)
	}

	// Required field — no code default. The devkit YAML MUST declare which
	// actions are gated so the behaviour is visible from the config alone.
	if config.Enabled && len(config.Actions) == 0 {
		return nil, fmt.Errorf(
			"validateVC: actions is required when enabled (e.g. \"confirm,init\")")
	}

	return config, nil
}

// IsActionEnabled reports whether the given beckn action is gated.
func (c *Config) IsActionEnabled(action string) bool {
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// IsMethodAllowed reports whether the given DID method (without the "did:"
// prefix, e.g. "key", "web") is permitted.
func (c *Config) IsMethodAllowed(method string) bool {
	for _, m := range c.AllowedDIDMethods {
		if m == method {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Credential extraction
// ---------------------------------------------------------------------------

// extractAction returns the beckn action from the URL path or the request
// body's context.action.
func extractAction(urlPath string, body []byte) string {
	parts := strings.Split(strings.TrimRight(urlPath, "/"), "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if last != "" && last != "caller" && last != "receiver" {
			return last
		}
	}
	var env struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		return env.Context.Action
	}
	return ""
}

// extractCredentials walks the parsed body and returns every embedded
// Verifiable Credential. A credential is recognised as a JSON object that
// carries both a "proof" and a "credentialSubject" — the combination beckn
// uses only for VCs (e.g. participantAttributes holding a
// MeterDataRequestCredential).
func extractCredentials(body []byte) []json.RawMessage {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	var out []json.RawMessage
	walkCredentials(root, &out)
	return out
}

func walkCredentials(node any, out *[]json.RawMessage) {
	switch n := node.(type) {
	case map[string]any:
		if isCredential(n) {
			if b, err := json.Marshal(n); err == nil {
				*out = append(*out, b)
			}
			// A credential's credentialSubject may itself embed nested
			// credentials in other domains; keep walking siblings but not the
			// already-captured subject to avoid double counting.
		}
		for _, v := range n {
			walkCredentials(v, out)
		}
	case []any:
		for _, v := range n {
			walkCredentials(v, out)
		}
	}
}

func isCredential(m map[string]any) bool {
	_, hasProof := m["proof"]
	_, hasSubject := m["credentialSubject"]
	return hasProof && hasSubject
}
