package vcvalidator

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
// On any failure the request is rejected with a beckn NACK and is NOT
// forwarded to the next handler.
type Config struct {
	// Enabled controls whether the plugin is active. When false the
	// middleware passes every request through untouched.
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

	// DebugLogging enables verbose per-credential logging.
	DebugLogging bool
}

// DefaultConfig returns a Config seeded with sensible defaults for the
// non-primary fields. The primary behaviour knob (Actions) is intentionally
// left empty — ParseConfig requires it in the YAML.
func DefaultConfig() *Config {
	return &Config{
		Enabled:           true,
		AllowedDIDMethods: []string{"key", "jwk", "web"},
		CheckExpiry:       true,
		CheckRevocation:   true,
		RequireProof:      true,
		FailOpen:          false,
		HTTPTimeout:       10 * time.Second,
		DebugLogging:      false,
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
			return nil, fmt.Errorf("vcvalidator: invalid httpTimeout %q", v)
		}
	}

	if v, ok := cfg["debugLogging"]; ok {
		config.DebugLogging = parseBool(v, config.DebugLogging)
	}

	// Required field — no code default. The devkit YAML MUST declare which
	// actions are gated so the behaviour is visible from the config alone.
	if config.Enabled && len(config.Actions) == 0 {
		return nil, fmt.Errorf(
			"vcvalidator: actions is required when enabled (e.g. \"confirm,init\")")
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
