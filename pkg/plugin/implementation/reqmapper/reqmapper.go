package reqmapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/jsonata-go/jsonata"
	"gopkg.in/yaml.v3"
)

// Config represents the configuration for the request mapper plugin.
type Config struct {
	Role         string `yaml:"role"`         // "bap" or "bpp"
	MappingsFile string `yaml:"mappingsFile"` // required path to mappings YAML
}

// MappingEngine handles JSONata-based transformations
type MappingEngine struct {
	config          *Config
	jsonataInstance jsonata.JSONataInstance
	bapMaps         map[string]jsonata.Expression
	bppMaps         map[string]jsonata.Expression
	mappings        map[string]builtinMapping
	mappingSource   string
	mutex           sync.RWMutex
	initialized     bool
}

type builtinMapping struct {
	BAP string `yaml:"bapMappings"`
	BPP string `yaml:"bppMappings"`
}

type mappingFile struct {
	Mappings map[string]builtinMapping `yaml:"mappings"`
}

var (
	engineInstance *MappingEngine
	engineOnce     sync.Once
)

type reqMapperStep struct {
	engine *MappingEngine
	role   string
}

type parsedRequest struct {
	req    map[string]interface{}
	action string
}

// NewReqMapper returns a middleware that maps requests using JSONata expressions.
func NewReqMapper(cfg *Config) (func(http.Handler) http.Handler, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	engine, err := initMappingEngine(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize mapping engine: %w", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}

			parsed, err := parseRequestBody(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			mappedBody, err := engine.Transform(r.Context(), parsed.action, parsed.req, cfg.Role)
			if err != nil {
				log.Errorf(r.Context(), err, "Transformation failed for action %s", parsed.action)
				mappedBody = body
			}

			r.Body = io.NopCloser(bytes.NewReader(mappedBody))
			r.ContentLength = int64(len(mappedBody))
			next.ServeHTTP(w, r)
		})
	}, nil
}

// NewReqMapperStep returns a handler step that applies the same reqmapper transformation logic.
func NewReqMapperStep(cfg *Config) (definition.Step, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	engine, err := initMappingEngine(cfg)
	if err != nil {
		return nil, err
	}
	return &reqMapperStep{
		engine: engine,
		role:   cfg.Role,
	}, nil
}

// Run transforms the current request body and updates the step context in place.
func (s *reqMapperStep) Run(ctx *model.StepContext) error {
	if ctx == nil {
		return model.NewBadReqErr(errors.New("step context cannot be nil"))
	}

	mappedBody, err := s.transformBody(ctx.Context, ctx.Body)
	if err != nil {
		return err
	}

	ctx.Body = mappedBody
	if ctx.Request != nil {
		ctx.Request.Body = io.NopCloser(bytes.NewReader(mappedBody))
		ctx.Request.ContentLength = int64(len(mappedBody))
	}

	return nil
}

func (s *reqMapperStep) transformBody(ctx context.Context, body []byte) ([]byte, error) {
	parsed, err := parseRequestBody(body)
	if err != nil {
		return nil, model.NewBadReqErr(err)
	}

	mappedBody, err := s.engine.Transform(ctx, parsed.action, parsed.req, s.role)
	if err != nil {
		log.Errorf(ctx, err, "Transformation failed for action %s", parsed.action)
		return body, nil
	}

	return mappedBody, nil
}

func parseRequestBody(body []byte) (*parsedRequest, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("Failed to decode request body")
	}

	reqContext, ok := req["context"].(map[string]interface{})
	if !ok {
		return nil, errors.New("context field not found or invalid")
	}

	action, ok := reqContext["action"].(string)
	if !ok || action == "" {
		return nil, errors.New("action field not found or invalid")
	}

	return &parsedRequest{
		req:    req,
		action: action,
	}, nil
}

// initMappingEngine initializes or returns existing mapping engine
func initMappingEngine(cfg *Config) (*MappingEngine, error) {
	var initErr error

	engineOnce.Do(func() {
		engineInstance = &MappingEngine{
			config:  cfg,
			bapMaps: make(map[string]jsonata.Expression),
			bppMaps: make(map[string]jsonata.Expression),
		}

		instance, err := jsonata.OpenLatest()
		if err != nil {
			initErr = fmt.Errorf("failed to initialize jsonata: %w", err)
			return
		}
		engineInstance.jsonataInstance = instance

		if err := engineInstance.loadBuiltinMappings(); err != nil {
			initErr = err
			return
		}

		engineInstance.initialized = true
	})

	if initErr != nil {
		return nil, initErr
	}

	if !engineInstance.initialized {
		return nil, errors.New("mapping engine failed to initialize")
	}

	return engineInstance, nil
}

func (e *MappingEngine) loadMappingsFromConfig() (map[string]builtinMapping, string, error) {
	if e.config == nil || e.config.MappingsFile == "" {
		return nil, "", errors.New("mappingsFile must be provided in config")
	}

	data, err := os.ReadFile(e.config.MappingsFile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read mappings file %s: %w", e.config.MappingsFile, err)
	}
	source := e.config.MappingsFile

	var parsed mappingFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, "", fmt.Errorf("failed to parse mappings from %s: %w", source, err)
	}

	if len(parsed.Mappings) == 0 {
		return nil, "", fmt.Errorf("no mappings found in %s", source)
	}

	return parsed.Mappings, source, nil
}

// loadBuiltinMappings compiles JSONata expressions for every action/direction pair from the configured mappings file.
func (e *MappingEngine) loadBuiltinMappings() error {
	mappings, source, err := e.loadMappingsFromConfig()
	if err != nil {
		return err
	}

	e.bapMaps = make(map[string]jsonata.Expression, len(mappings))
	e.bppMaps = make(map[string]jsonata.Expression, len(mappings))

	for action, mapping := range mappings {
		bapExpr, err := e.jsonataInstance.Compile(mapping.BAP, false)
		if err != nil {
			return fmt.Errorf("failed to compile BAP mapping for action %s: %w", action, err)
		}
		bppExpr, err := e.jsonataInstance.Compile(mapping.BPP, false)
		if err != nil {
			return fmt.Errorf("failed to compile BPP mapping for action %s: %w", action, err)
		}

		e.bapMaps[action] = bapExpr
		e.bppMaps[action] = bppExpr
	}

	e.mappings = mappings
	e.mappingSource = source

	log.Infof(
		context.Background(),
		"Loaded %d BAP mappings and %d BPP mappings from %s",
		len(e.bapMaps),
		len(e.bppMaps),
		source,
	)

	return nil
}

// Transform applies the appropriate mapping based on role and action
func (e *MappingEngine) Transform(ctx context.Context, action string, req map[string]interface{}, role string) ([]byte, error) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	var expr jsonata.Expression
	var found bool

	// Select appropriate mapping based on role
	switch role {
	case "bap":
		expr, found = e.bapMaps[action]
	case "bpp":
		expr, found = e.bppMaps[action]
	default:
		return json.Marshal(req)
	}

	// If no mapping found, return original request
	if !found || expr == nil {
		log.Debugf(ctx, "No mapping found for action: %s, role: %s", action, role)
		return json.Marshal(req)
	}

	// Marshal request for JSONata evaluation
	input, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request for mapping: %w", err)
	}

	// Apply JSONata transformation
	result, err := expr.Evaluate(input, nil)
	if err != nil {
		return nil, fmt.Errorf("JSONata evaluation failed: %w", err)
	}

	log.Debugf(ctx, "Successfully transformed %s request using %s mapping, %s", action, role, result)
	return result, nil
}

// ReloadMappings reloads all mapping files (useful for hot-reload scenarios)
func (e *MappingEngine) ReloadMappings() error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	return e.loadBuiltinMappings()
}

// GetMappingInfo returns information about loaded mappings
func (e *MappingEngine) GetMappingInfo() map[string]interface{} {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	bapActions := make([]string, 0, len(e.bapMaps))
	for action := range e.bapMaps {
		bapActions = append(bapActions, action)
	}

	bppActions := make([]string, 0, len(e.bppMaps))
	for action := range e.bppMaps {
		bppActions = append(bppActions, action)
	}

	return map[string]interface{}{
		"bap_mappings":    bapActions,
		"bpp_mappings":    bppActions,
		"mappings_source": e.mappingSource,
		"action_count":    len(e.mappings),
	}
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config cannot be nil")
	}
	if cfg.Role != "bap" && cfg.Role != "bpp" {
		return errors.New("role must be either 'bap' or 'bpp'")
	}
	if cfg.MappingsFile == "" {
		return errors.New("mappingsFile is required")
	}
	return nil
}
