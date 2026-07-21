package reqmapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/jsonata-go/jsonata"
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

func TestReqMapperStepRun_FallsBackToOriginalBodyWhenTransformFails(t *testing.T) {
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

	require.NoError(t, step.Run(ctx))
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
	requireBadReqCode(t, err, "SCH_INVALID_JSON")
}

func TestParseRequestBody(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		_, err := parseRequestBody([]byte("{"))
		require.Error(t, err)
		requireBadReqCode(t, err, "SCH_INVALID_JSON")
	})

	t.Run("missing context", func(t *testing.T) {
		_, err := parseRequestBody([]byte(`{"message":{}}`))
		require.EqualError(t, err, "context field not found or invalid")
		requireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})

	t.Run("missing action", func(t *testing.T) {
		_, err := parseRequestBody([]byte(`{"context":{},"message":{}}`))
		require.EqualError(t, err, "action field not found or invalid")
		requireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})

	t.Run("empty action", func(t *testing.T) {
		_, err := parseRequestBody([]byte(`{"context":{"action":""},"message":{}}`))
		require.EqualError(t, err, "action field not found or invalid")
		requireBadReqCode(t, err, "SCH_REQUIRED_FIELD_MISSING")
	})
}

// requireBadReqCode confirms err is a *model.BadReqErr classified with wantCode.
func requireBadReqCode(t *testing.T, err error, wantCode string) {
	t.Helper()

	badReqErr, ok := err.(*model.BadReqErr)
	if !ok {
		t.Fatalf("expected *model.BadReqErr, got %T: %v", err, err)
	}
	if code := badReqErr.BecknError().Code; code != wantCode {
		t.Errorf("BecknError().Code = %s, want %s", code, wantCode)
	}
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
