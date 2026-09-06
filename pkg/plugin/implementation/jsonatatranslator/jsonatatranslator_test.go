package jsonatatranslator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	v206 "github.com/jsonata-go/jsonata/v206"
	"github.com/stretchr/testify/require"

	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

func newTestTranslator(t *testing.T) *Translator {
	t.Helper()
	translator, closer, err := New(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, closer)
	t.Cleanup(func() { require.NoError(t, closer()) })
	return translator.(*Translator)
}

func TestTranslate_Success(t *testing.T) {
	translator := newTestTranslator(t)

	payload := []byte(`{"a": 1, "b": 2}`)
	result, err := translator.Translate(context.Background(), []byte(`{"sum": a + b}`), payload)
	require.NoError(t, err)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &out))
	require.Equal(t, float64(3), out["sum"])
}

func TestTranslate_CompileError(t *testing.T) {
	translator := newTestTranslator(t)

	_, err := translator.Translate(context.Background(), []byte(`(((`), []byte(`{}`))
	require.Error(t, err)
}

func TestTranslate_EvaluateError(t *testing.T) {
	translator := newTestTranslator(t)

	// $number("not-a-number") compiles fine but always fails evaluation
	// with a real v206 D3030 error.
	_, err := translator.Translate(context.Background(), []byte(`$number("not-a-number")`), []byte(`{}`))
	require.Error(t, err)

	var jsonataErr *v206.JSONataError
	require.ErrorAs(t, err, &jsonataErr, "expected errors.As to reach the underlying *v206.JSONataError")
	require.Equal(t, "D3030", jsonataErr.Code)
}

func TestTranslate_CachesCompiledExpression(t *testing.T) {
	translator := newTestTranslator(t)

	expr := []byte(`$`)
	_, err := translator.Translate(context.Background(), expr, []byte(`{"x":1}`))
	require.NoError(t, err)
	require.Len(t, translator.exprs.entries, 1)

	_, err = translator.Translate(context.Background(), expr, []byte(`{"x":2}`))
	require.NoError(t, err)
	require.Len(t, translator.exprs.entries, 1, "expression should be reused from cache, not recompiled")
}

func TestTranslate_ConcurrentAccess(t *testing.T) {
	translator := newTestTranslator(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := translator.Translate(context.Background(), []byte(`$`), []byte(`{"x":1}`))
			require.NoError(t, err)
		}()
	}
	wg.Wait()
}

func TestProvider_ImplementsTranslatorProvider(t *testing.T) {
	var _ definition.TranslatorProvider = Provider
}

func TestProvider_New(t *testing.T) {
	translator, closer, err := Provider.New(context.Background(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, translator)
	require.NotNil(t, closer)
	require.NoError(t, closer())
}
