// Package jsonmapper transforms one JSON document into another using a JSONata
// mapping fetched at runtime, so that translating between two parties' shapes is
// configuration rather than code.
//
// It is domain-free by design: it knows nothing about who is calling, nothing
// about what a mapping says, and nothing about the payloads passing through. It
// is handed a reference and an input, and it fetches, compiles, caches and runs
// whatever is there. Anything specific to a network or a provider belongs in the
// caller, which is what lets one mapper serve all of them.
package jsonmapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/jsonata-go/jsonata"
	"gopkg.in/yaml.v2"
)

// Defaults applied when an operator leaves a setting out.
const (
	DefaultFetchTimeout    = 5 * time.Second
	DefaultMaxMappingBytes = 256 << 10 // 256 KiB, far above any realistic mapping
	DefaultCacheTTL        = time.Hour
	DefaultNegativeTTL     = time.Minute
	DefaultMaxCacheEntries = 200
)

// codeAdaptationFailed reports a mapping that ran but could not produce a
// result. It is the payload's shape that is wrong, so it is a bad request
// rather than a fault of this adapter.
const codeAdaptationFailed = "SCH_SCHEMA_ADAPTATION_FAILED"

// mappingFile is the published form of a mapping: every action one capability
// serves, in one file, keyed by action name.
//
// Request files are keyed by the action they translate ("select"); response
// files by the action they produce ("on_select"). Each file therefore names the
// Beckn actions it deals in, and the filename says nothing -- naming a file
// after one action while it serves several would be worse than not naming it.
//
// One file per direction rather than per action means a transaction walking
// select then confirm pays one fetch, not one per step.
type mappingFile struct {
	Actions map[string]string `yaml:"actions"`
}

// Config holds configuration parameters for the mapper.
type Config struct {
	// FetchTimeout bounds a single mapping fetch. A mapping host that accepts
	// the connection and then goes quiet must not hold a request open.
	FetchTimeout time.Duration `yaml:"fetchTimeout" json:"fetchTimeout"`

	// MaxMappingBytes caps what is read from a mapping host. References come
	// from the registry, so an unbounded read is an unbounded allocation driven
	// by whoever can write a registry record.
	MaxMappingBytes int64 `yaml:"maxMappingBytes" json:"maxMappingBytes"`

	// CacheTTL is how long a compiled mapping is reused. It is also how long a
	// corrected mapping takes to take effect.
	CacheTTL time.Duration `yaml:"cacheTTL" json:"cacheTTL"`

	// NegativeTTL is how long a failed fetch is remembered. Without it a broken
	// reference turns every inbound request into an outbound one.
	NegativeTTL time.Duration `yaml:"negativeTTL" json:"negativeTTL"`

	// MaxCacheEntries bounds the cache, which would otherwise grow with the
	// number of capabilities ever seen.
	MaxCacheEntries int `yaml:"maxCacheEntries" json:"maxCacheEntries"`
}

// cacheEntry is one compiled mapping, or the failure that stopped it compiling.
// Failures are cached too, which is the whole point of the negative TTL.
//
// The mutex guards evaluation, not the entry: jsonata.Expression.Evaluate
// mutates the expression it is called on -- it binds into the expression's own
// frame -- so one compiled mapping cannot serve two requests at once. Confirmed
// with the race detector, not assumed.
//
// Evaluating under a lock rather than compiling per request is the cheaper
// trade by a wide margin: evaluation is ~22us against ~184us to compile, and
// both are dwarfed by the upstream call the mapped request goes on to make. The
// lock is per mapping, so different mappings still run in parallel. A pool of
// compiled expressions would remove even that, and is the upgrade if one
// mapping ever becomes hot enough to matter.
type cacheEntry struct {
	// actions holds one compiled mapping per action the file serves. A file is
	// fetched and compiled as a whole, so every action it declares is ready
	// after the first request for any of them.
	actions map[string]*compiledAction
	// err is a failure that applies to the whole file -- it could not be
	// fetched, or not parsed -- as opposed to one action failing to compile.
	err       error
	expiresAt time.Time
}

// compiledAction is one action's mapping, or the failure that stopped it
// compiling. Failures are held per action deliberately: a typo in confirm is no
// reason for select to stop being served.
type compiledAction struct {
	expression jsonata.Expression
	evaluating *sync.Mutex
	err        error
}

// Mapper fetches, compiles and runs mappings. It is safe for concurrent use:
// one mapper serves every inbound request.
type Mapper struct {
	config     *Config
	httpClient *http.Client
	instance   jsonata.JSONataInstance

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

// New creates a Mapper, applying defaults for anything left unset.
func New(ctx context.Context, cfg *Config) (*Mapper, func() error, error) {
	if cfg == nil {
		return nil, nil, errors.New("jsonmapper: config cannot be nil")
	}
	applyDefaults(cfg)

	instance, err := jsonata.OpenLatest()
	if err != nil {
		return nil, nil, fmt.Errorf("jsonmapper: failed to open jsonata: %w", err)
	}

	mapper := &Mapper{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.FetchTimeout},
		instance:   instance,
		entries:    make(map[string]cacheEntry),
	}

	closer := func() error {
		log.Debugf(ctx, "Cleaning up JSON mapper resources")
		mapper.httpClient.CloseIdleConnections()
		return nil
	}

	log.Infof(ctx, "JSON mapper created successfully")
	return mapper, closer, nil
}

// applyDefaults fills in every setting an operator left out.
func applyDefaults(cfg *Config) {
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = DefaultFetchTimeout
	}
	if cfg.MaxMappingBytes <= 0 {
		cfg.MaxMappingBytes = DefaultMaxMappingBytes
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = DefaultCacheTTL
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = DefaultNegativeTTL
	}
	if cfg.MaxCacheEntries <= 0 {
		cfg.MaxCacheEntries = DefaultMaxCacheEntries
	}
}

// Transform runs the mapping at mappingRef over input.
func (m *Mapper) Transform(ctx context.Context, mappingRef, action string, input any) ([]byte, error) {
	if action == "" {
		return nil, fmt.Errorf("jsonmapper: cannot resolve a mapping in %q without an action", mappingRef)
	}

	entry, err := m.compiled(ctx, mappingRef)
	if err != nil {
		return nil, err
	}

	mapping, served := entry.actions[action]
	if !served {
		// Naming what the file does serve turns a deploy mistake into a one-line
		// fix, rather than a hunt through registry rows.
		return nil, fmt.Errorf("jsonmapper: mapping %q does not serve action %q; it serves %s",
			mappingRef, action, strings.Join(servedActions(entry), ", "))
	}
	if mapping.err != nil {
		return nil, mapping.err
	}
	return m.evaluate(ctx, mapping, mappingRef, action, input)
}

// servedActions lists the actions a file serves, in a stable order so the same
// mistake reads the same way twice.
func servedActions(entry cacheEntry) []string {
	names := make([]string, 0, len(entry.actions))
	for name := range entry.actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compiled returns the compiled mapping for a reference, fetching and compiling
// it on first use. A failure is cached too, for a shorter time.
func (m *Mapper) compiled(ctx context.Context, mappingRef string) (cacheEntry, error) {
	if entry, found := m.cached(mappingRef); found {
		return entry, entry.err
	}

	actions, err := m.fetchAndCompile(ctx, mappingRef)
	return m.remember(mappingRef, actions, err), err
}

// cached returns a live cache entry, if there is one.
func (m *Mapper) cached(mappingRef string) (cacheEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, found := m.entries[mappingRef]
	if !found || time.Now().After(entry.expiresAt) {
		return cacheEntry{}, false
	}
	return entry, true
}

// remember caches a compiled mapping, or the failure that stopped it compiling.
// A failure gets the shorter TTL: it should stop hammering a broken reference
// without outlasting the fix.
func (m *Mapper) remember(mappingRef string, actions map[string]*compiledAction, err error) cacheEntry {
	ttl := m.config.CacheTTL
	if err != nil {
		ttl = m.config.NegativeTTL
	}
	entry := cacheEntry{
		actions:   actions,
		err:       err,
		expiresAt: time.Now().Add(ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Bounded rather than evicting: references come from the registry, and a
	// deployment serving more capabilities than the cap wants a bigger cap, not
	// a cache that silently thrashes. The entry is still returned to its caller
	// when it is not stored, so a request over the cap is served rather than
	// refused -- it just pays to compile again next time.
	if len(m.entries) >= m.config.MaxCacheEntries {
		if _, replacing := m.entries[mappingRef]; !replacing {
			return entry
		}
	}
	m.entries[mappingRef] = entry
	return entry
}

// cachedCount reports how many mappings are held. Used by tests to assert the
// cache stays bounded.
func (m *Mapper) cachedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// fetchAndCompile retrieves a mapping and turns it into a runnable expression.
func (m *Mapper) fetchAndCompile(ctx context.Context, mappingRef string) (map[string]*compiledAction, error) {
	body, err := m.fetch(ctx, mappingRef)
	if err != nil {
		return nil, err
	}
	sources, err := parseActions(body)
	if err != nil {
		return nil, fmt.Errorf("jsonmapper: mapping %q: %w", mappingRef, err)
	}

	// Every action is compiled now rather than on first use, so one fetch
	// leaves the whole file ready. A compile failure is recorded against its own
	// action and goes no further than that action.
	actions := make(map[string]*compiledAction, len(sources))
	for action, source := range sources {
		actions[action] = m.compileAction(ctx, mappingRef, action, source)
	}
	log.Debugf(ctx, "JSON mapper compiled %d action(s) from mapping: %s", len(actions), mappingRef)
	return actions, nil
}

// compileAction compiles one action's mapping, keeping any failure local to it.
func (m *Mapper) compileAction(ctx context.Context, mappingRef, action, source string) *compiledAction {
	if strings.TrimSpace(source) == "" {
		// Declared, but with nothing to build. That is a statement rather than an
		// omission -- see definition.ErrNoTransform -- so it is held as this
		// action's outcome and reported to whoever asks for it, while every other
		// action in the file is unaffected.
		return &compiledAction{err: fmt.Errorf("jsonmapper: mapping %q action %q: %w",
			mappingRef, action, definition.ErrNoTransform)}
	}
	expression, err := m.instance.Compile(source, false)
	if err != nil {
		log.Errorf(ctx, err, "JSON mapper could not compile action %s of %s: %v", action, mappingRef, err)
		return &compiledAction{err: fmt.Errorf("jsonmapper: mapping %q action %q failed to compile: %w", mappingRef, action, err)}
	}
	return &compiledAction{expression: expression, evaluating: &sync.Mutex{}}
}

// fetch retrieves a mapping's bytes, bounded in both time and size.
func (m *Mapper) fetch(ctx context.Context, mappingRef string) ([]byte, error) {
	if err := verifyFetchable(mappingRef); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mappingRef, nil)
	if err != nil {
		return nil, fmt.Errorf("jsonmapper: failed to build request for mapping %q: %w", mappingRef, err)
	}

	log.Debugf(ctx, "Fetching mapping: %s", mappingRef)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jsonmapper: failed to fetch mapping %q: %w", mappingRef, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Errorf(ctx, nil, "Mapping fetch failed with status: %s, mapping: %s", resp.Status, mappingRef)
		return nil, fmt.Errorf("jsonmapper: mapping %q returned %s", mappingRef, resp.Status)
	}

	// LimitReader with one spare byte, so an oversized body is refused rather
	// than silently truncated into a mapping that compiles to something else.
	body, err := io.ReadAll(io.LimitReader(resp.Body, m.config.MaxMappingBytes+1))
	if err != nil {
		return nil, fmt.Errorf("jsonmapper: failed to read mapping %q: %w", mappingRef, err)
	}
	if int64(len(body)) > m.config.MaxMappingBytes {
		return nil, fmt.Errorf("jsonmapper: mapping %q exceeds the %d byte limit", mappingRef, m.config.MaxMappingBytes)
	}
	return body, nil
}

// verifyFetchable rejects a reference this mapper will not retrieve.
//
// References come from the registry, which makes them external input: an
// unchecked one would let a registry record name a file path or an internal
// scheme and have the adapter read it.
func verifyFetchable(mappingRef string) error {
	if mappingRef == "" {
		return errors.New("jsonmapper: mapping reference is empty")
	}
	parsed, err := url.Parse(mappingRef)
	if err != nil {
		return fmt.Errorf("jsonmapper: invalid mapping reference %q: %w", mappingRef, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("jsonmapper: mapping reference %q must be http or https", mappingRef)
	}
	if parsed.Host == "" {
		return fmt.Errorf("jsonmapper: mapping reference %q names no host", mappingRef)
	}
	return nil
}

// parseActions reads the actions a published mapping serves.
func parseActions(body []byte) (map[string]string, error) {
	var file mappingFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("could not be parsed: %w", err)
	}
	if len(file.Actions) == 0 {
		return nil, errors.New("serves no actions")
	}
	return file.Actions, nil
}

// marshalInput renders the named inputs a mapping reads -- beckn, _local and,
// on the response leg, response -- as the single JSON document JSONata
// evaluates against.
func marshalInput(input any) ([]byte, error) {
	document, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("input could not be encoded: %w", err)
	}
	return document, nil
}

// evaluate runs a compiled mapping over the input document.
func (m *Mapper) evaluate(ctx context.Context, mapping *compiledAction, mappingRef, action string, input any) ([]byte, error) {
	document, err := marshalInput(input)
	if err != nil {
		return nil, fmt.Errorf("jsonmapper: mapping %q: %w", mappingRef, err)
	}

	// See compiledAction: Evaluate mutates the expression, so one action's
	// mapping serves one request at a time. Other actions in the same file are
	// unaffected, and marshalling above is deliberately outside the lock.
	mapping.evaluating.Lock()
	result, err := mapping.expression.Evaluate(document, nil)
	mapping.evaluating.Unlock()
	if err != nil {
		// The mapping is valid and the payload is not what it expected, so this
		// is the caller's request being wrong rather than this adapter failing.
		log.Errorf(ctx, err, "JSON mapping %s action %s failed to evaluate: %v", mappingRef, action, err)
		return nil, model.NewBadReqErr(codeAdaptationFailed,
			fmt.Errorf("mapping %q action %q could not be applied: %w", mappingRef, action, err))
	}
	return result, nil
}
