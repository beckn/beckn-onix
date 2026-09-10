// Package jsonatatranslator implements definition.Translator using the
// JSONata expression language.
package jsonatatranslator

import (
	"context"
	"fmt"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/jsonata-go/jsonata"
)

// defaultMaxExprCacheEntries caps the compiled-expression cache. Expressions
// are deterministic and never expire; the cap just bounds memory on nodes
// that see many distinct artifacts.
const defaultMaxExprCacheEntries = 500

// exprCache stores compiled JSONata expressions keyed by the raw artifact
// text. Entries never expire; once the cap is reached, new expressions are
// still compiled and evaluated but not cached.
type exprCache struct {
	mu      sync.RWMutex
	entries map[string]*cachedExpr
	max     int
}

// cachedExpr pairs a compiled expression with a mutex serializing Evaluate
// calls on it — jsonata.Expression.Evaluate mutates the compiled
// expression's own state (its environment, its timestamp), so concurrent
// calls on the same expression race.
type cachedExpr struct {
	mu   sync.Mutex
	expr jsonata.Expression
}

func newExprCache(max int) *exprCache {
	return &exprCache{entries: make(map[string]*cachedExpr), max: max}
}

// Translator implements definition.Translator by compiling (with caching)
// and evaluating JSONata expressions. The artifact passed to Translate is the
// raw JSONata expression text; payload is the JSON fragment to transform.
type Translator struct {
	instance jsonata.JSONataInstance
	exprs    *exprCache
}

// New constructs a Translator. config has no recognised keys yet; accepted
// for interface consistency.
func New(_ context.Context, _ map[string]string) (definition.Translator, func() error, error) {
	instance, err := jsonata.OpenLatest()
	if err != nil {
		return nil, nil, fmt.Errorf("jsonatatranslator: open jsonata: %w", err)
	}
	t := &Translator{
		instance: instance,
		exprs:    newExprCache(defaultMaxExprCacheEntries),
	}
	return t, func() error { return nil }, nil
}

// Translate compiles (with caching) the JSONata expression carried in
// artifact and evaluates it against payload, returning the transformed JSON
// bytes. ctx is accepted for interface consistency; jsonata-go does not
// support context cancellation, so it is not forwarded to the evaluator.
//
// Access to the cached expression is serialized — see cachedExpr. The
// non-nil bindings map also makes Evaluate allocate a fresh per-call frame
// instead of reusing the expression's shared one.
func (t *Translator) Translate(_ context.Context, artifact []byte, payload []byte) ([]byte, error) {
	ce, err := t.compiledExpr(artifact)
	if err != nil {
		return nil, fmt.Errorf("jsonatatranslator: compile expression: %w", err)
	}
	ce.mu.Lock()
	defer ce.mu.Unlock()
	result, err := ce.expr.Evaluate(payload, map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("jsonatatranslator: evaluate expression: %w", err)
	}
	return result, nil
}

// compiledExpr returns a cached compiled JSONata expression for artifact,
// compiling and caching it on a miss. Indexing the map with artifact
// directly avoids a string allocation on a hit.
func (t *Translator) compiledExpr(artifact []byte) (*cachedExpr, error) {
	t.exprs.mu.RLock()
	ce, ok := t.exprs.entries[string(artifact)]
	t.exprs.mu.RUnlock()
	if ok {
		return ce, nil
	}

	expression := string(artifact)
	expr, err := t.instance.Compile(expression, false)
	if err != nil {
		return nil, err
	}
	ce = &cachedExpr{expr: expr}

	t.exprs.mu.Lock()
	if len(t.exprs.entries) < t.exprs.max {
		t.exprs.entries[expression] = ce
	}
	t.exprs.mu.Unlock()
	return ce, nil
}

// provider implements definition.TranslatorProvider.
type provider struct{}

func (p provider) New(ctx context.Context, config map[string]string) (definition.Translator, func() error, error) {
	return New(ctx, config)
}

// Provider is the exported symbol the plugin manager looks up.
var Provider = provider{}
