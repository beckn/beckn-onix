package reqmapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/testutil"
	"github.com/jsonata-go/jsonata"
	v206 "github.com/jsonata-go/jsonata/v206"
	"github.com/stretchr/testify/require"
)

type failingExpression struct{}

func (failingExpression) Evaluate(inputJSON []byte, bindings map[string]interface{}) ([]byte, error) {
	return nil, errors.New("boom")
}

func (failingExpression) SetMaxDepth(maxDepth int) {}

func (failingExpression) SetMaxTime(maxMs int) {}

func (failingExpression) SetMaxRange(maxRange int) {}

func (failingExpression) Assign(name string, value interface{}) {}

func (failingExpression) RegisterFunction(name string, implementation interface{}, signature string) error {
	return nil
}

func (failingExpression) AST() interface{} { return nil }

func (failingExpression) Errors() []error { return nil }

var _ jsonata.Expression = failingExpression{}

// jsonataErrExpression fails Evaluate with a *v206.JSONataError carrying the
// given Code, for driving classifyEvaluateErr's branches directly.
type jsonataErrExpression struct {
	failingExpression
	code string
}

func (e jsonataErrExpression) Evaluate(inputJSON []byte, bindings map[string]interface{}) ([]byte, error) {
	return nil, &v206.JSONataError{Code: e.code, Message: "synthetic error for test"}
}

var _ jsonata.Expression = jsonataErrExpression{}

func testMappingsFile() string {
	return filepath.Join("testdata", "mappings.yaml")
}

func newTestEngine(t *testing.T) *MappingEngine {
	t.Helper()

	engine, err := initMappingEngine(&Config{
		Role:         "bap",
		MappingsFile: testMappingsFile(),
	})
	require.NoError(t, err)
	return engine
}

func testSearchPayload(t *testing.T) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]interface{}{
		"context": map[string]interface{}{
			"action": "search",
		},
		"message": map[string]interface{}{
			"intent": map[string]interface{}{
				"fulfillment": map[string]interface{}{
					"start": map[string]interface{}{
						"location": map[string]interface{}{
							"gps": "12.9716,77.5946",
						},
					},
					"end": map[string]interface{}{
						"location": map[string]interface{}{
							"gps": "13.0827,80.2707",
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return body
}

func TestNewReqMapperStep(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		step, err := NewReqMapperStep(nil)
		require.Error(t, err)
		require.Nil(t, step)
	})

	t.Run("invalid role", func(t *testing.T) {
		step, err := NewReqMapperStep(&Config{
			Role:         "invalid",
			MappingsFile: testMappingsFile(),
		})
		require.Error(t, err)
		require.Nil(t, step)
	})

	t.Run("valid config", func(t *testing.T) {
		step, err := NewReqMapperStep(&Config{
			Role:         "bap",
			MappingsFile: testMappingsFile(),
		})
		require.NoError(t, err)
		require.NotNil(t, step)
	})
}

func TestReqMapperStepRun_Success(t *testing.T) {
	step, err := NewReqMapperStep(&Config{
		Role:         "bap",
		MappingsFile: testMappingsFile(),
	})
	require.NoError(t, err)

	body := testSearchPayload(t)
	req, err := http.NewRequest(http.MethodPost, "http://example.com/search", bytes.NewReader(body))
	require.NoError(t, err)
	req.TransferEncoding = []string{"chunked"}

	ctx := &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    body,
	}

	require.NoError(t, step.Run(ctx))
	require.NotEqual(t, string(body), string(ctx.Body))
	require.Equal(t, int64(len(ctx.Body)), ctx.Request.ContentLength)
	require.Nil(t, ctx.Request.TransferEncoding)
	require.NotNil(t, ctx.Request.GetBody)

	var transformed map[string]interface{}
	require.NoError(t, json.Unmarshal(ctx.Body, &transformed))
	message, ok := transformed["message"].(map[string]interface{})
	require.True(t, ok)
	_, ok = message["filters"].(map[string]interface{})
	require.True(t, ok)

	clonedBody, err := ctx.Request.GetBody()
	require.NoError(t, err)
	defer clonedBody.Close()
	clonedBytes, err := io.ReadAll(clonedBody)
	require.NoError(t, err)
	require.Equal(t, string(ctx.Body), string(clonedBytes))
}

func TestReqMapperStepRun_TransformFailureNacksInsteadOfFallingBack(t *testing.T) {
	step := &reqMapperStep{
		role: "bap",
		engine: &MappingEngine{
			bapMaps: map[string]jsonata.Expression{
				"search": failingExpression{},
			},
			bppMaps: make(map[string]jsonata.Expression),
			mutex:   sync.RWMutex{},
		},
	}

	body := testSearchPayload(t)
	req, err := http.NewRequest(http.MethodPost, "http://example.com/search", bytes.NewReader(body))
	require.NoError(t, err)

	ctx := &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    body,
	}

	err = step.Run(ctx)
	require.Error(t, err)
	testutil.RequireBadReqCode(t, err, "BIZ_GENERIC_ERROR")
	// Run returns before assigning ctx.Body on failure, so it's untouched.
	require.Equal(t, string(body), string(ctx.Body))
}

func TestReqMapperStepRun_EmptyBody(t *testing.T) {
	step, err := NewReqMapperStep(&Config{
		Role:         "bap",
		MappingsFile: testMappingsFile(),
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "http://example.com/search", bytes.NewReader(nil))
	require.NoError(t, err)
	ctx := &model.StepContext{
		Context: context.Background(),
		Request: req,
		Body:    nil,
	}

	err = step.Run(ctx)
	require.Error(t, err)
	testutil.RequireBadReqCode(t, err, "SCH_INVALID_JSON")
}

func TestParseRequestBody(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		_, err := parseRequestBody([]byte("{"))
		require.Error(t, err)
		testutil.RequireBadReqCode(t, err, "SCH_INVALID_JSON")

		// Regression test: the decode failure must still unwrap to the
		// underlying json error via errors.As, matching the wrapping this
		// function did before model.ExtractContext was extracted (self-review
		// of #882 found this had silently regressed, then fixed it).
		var syntaxErr *json.SyntaxError
		require.ErrorAs(t, err, &syntaxErr, "expected errors.As to still reach the underlying *json.SyntaxError")
	})

	t.Run("missing context", func(t *testing.T) {
		// The exact message text for this classification is model.ExtractContext's
		// concern and is already verified directly in pkg/model/model_test.go;
		// this only needs to confirm parseRequestBody forwards the Code correctly.
		_, err := parseRequestBody([]byte(`{"message":{}}`))
		testutil.RequireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})

	t.Run("missing action", func(t *testing.T) {
		_, err := parseRequestBody([]byte(`{"context":{},"message":{}}`))
		require.EqualError(t, err, "action field not found or invalid")
		testutil.RequireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})

	t.Run("empty action", func(t *testing.T) {
		_, err := parseRequestBody([]byte(`{"context":{"action":""},"message":{}}`))
		require.EqualError(t, err, "action field not found or invalid")
		testutil.RequireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})
}

func TestMappingEngineTransform(t *testing.T) {
	engine := newTestEngine(t)

	t.Run("unknown action falls back", func(t *testing.T) {
		req := map[string]interface{}{
			"context": map[string]interface{}{"action": "unknown"},
			"message": map[string]interface{}{},
		}

		expected, err := json.Marshal(req)
		require.NoError(t, err)

		result, err := engine.Transform(context.Background(), "unknown", req, "bap")
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(result))
	})

	t.Run("unknown role falls back", func(t *testing.T) {
		req := map[string]interface{}{
			"context": map[string]interface{}{"action": "search"},
			"message": map[string]interface{}{},
		}

		expected, err := json.Marshal(req)
		require.NoError(t, err)

		result, err := engine.Transform(context.Background(), "search", req, "unknown")
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(result))
	})

	t.Run("marshal failure ahead of jsonata evaluation classifies as BIZ_GENERIC_ERROR", func(t *testing.T) {
		req := map[string]interface{}{
			"context": map[string]interface{}{"action": "search"},
			// math.Inf isn't representable in JSON, so json.Marshal fails
			// here without the JSONata engine ever being reached.
			"message": map[string]interface{}{"badValue": math.Inf(1)},
		}

		_, err := engine.Transform(context.Background(), "search", req, "bap")
		require.Error(t, err)
		testutil.RequireBadReqCode(t, err, "BIZ_GENERIC_ERROR")
	})

	// jsonata_dynamic_error (testdata/mappings.yaml) triggers a real v206 D3030 error,
	// exercising the actual JSONata T*/D* error path instead of a mock.
	t.Run("genuine JSONata dynamic error classifies as SCH_SCHEMA_ADAPTATION_FAILED", func(t *testing.T) {
		req := map[string]interface{}{
			"context": map[string]interface{}{"action": "jsonata_dynamic_error"},
		}

		_, err := engine.Transform(context.Background(), "jsonata_dynamic_error", req, "bap")
		require.Error(t, err)
		testutil.RequireBadReqCode(t, err, "SCH_SCHEMA_ADAPTATION_FAILED")

		var jsonataErr *v206.JSONataError
		require.ErrorAs(t, err, &jsonataErr, "expected errors.As to still reach the underlying *v206.JSONataError")
		require.Equal(t, "D3030", jsonataErr.Code)
	})
}

func TestMappingEngineTransform_ClassifiesEvaluateFailures(t *testing.T) {
	tests := []struct {
		name     string
		expr     jsonata.Expression
		wantCode string
	}{
		{"type-mismatch JSONataError (T*) -> SCH_SCHEMA_ADAPTATION_FAILED", jsonataErrExpression{code: "T2001"}, "SCH_SCHEMA_ADAPTATION_FAILED"},
		{"dynamic JSONataError (D*) -> SCH_SCHEMA_ADAPTATION_FAILED", jsonataErrExpression{code: "D3030"}, "SCH_SCHEMA_ADAPTATION_FAILED"},
		{"recursion-depth JSONataError (U1001) -> BIZ_GENERIC_ERROR", jsonataErrExpression{code: "U1001"}, "BIZ_GENERIC_ERROR"},
		{"timeout JSONataError (U1002) -> BIZ_GENERIC_ERROR", jsonataErrExpression{code: "U1002"}, "BIZ_GENERIC_ERROR"},
		{"panic-recovery JSONataError (U1003) -> BIZ_GENERIC_ERROR", jsonataErrExpression{code: "U1003"}, "BIZ_GENERIC_ERROR"},
		{"non-JSONataError cause -> BIZ_GENERIC_ERROR", failingExpression{}, "BIZ_GENERIC_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &MappingEngine{
				bapMaps: map[string]jsonata.Expression{"search": tt.expr},
				bppMaps: make(map[string]jsonata.Expression),
				mutex:   sync.RWMutex{},
			}
			req := map[string]interface{}{"context": map[string]interface{}{"action": "search"}}

			_, err := engine.Transform(context.Background(), "search", req, "bap")
			require.Error(t, err)
			testutil.RequireBadReqCode(t, err, tt.wantCode)
		})
	}
}

func TestMappingEngineReloadMappings(t *testing.T) {
	engine := newTestEngine(t)
	originalBAP := len(engine.bapMaps)
	originalBPP := len(engine.bppMaps)
	require.NotZero(t, originalBAP)
	require.NotZero(t, originalBPP)

	for action := range engine.bapMaps {
		delete(engine.bapMaps, action)
		break
	}
	require.NotEqual(t, originalBAP, len(engine.bapMaps))

	require.NoError(t, engine.ReloadMappings())
	require.Equal(t, originalBAP, len(engine.bapMaps))
	require.Equal(t, originalBPP, len(engine.bppMaps))
}
