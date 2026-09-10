package reqmapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	v206 "github.com/jsonata-go/jsonata/v206"
	"gopkg.in/yaml.v3"
)

// codeSchemaAdaptationFailed is used for JSONata evaluation failures that
// are data/shape-driven (T*/D* JSONataError codes).
const codeSchemaAdaptationFailed = "SCH_SCHEMA_ADAPTATION_FAILED"

// codeBizGenericError is the fallback for transform failures that aren't a
// shape mismatch: request marshalling, or a JSONata resource error (U*
// codes) rather than a T*/D* one.
const codeBizGenericError = "BIZ_GENERIC_ERROR"

// Config represents the configuration for the request mapper plugin.
type Config struct {
	Role         string `yaml:"role"`         // "bap" or "bpp"
	MappingsFile string `yaml:"mappingsFile"` // required path to mappings YAML
}

// MappingEngine loads mapping artifacts from the configured mappings file
// and delegates their execution to an injected Translator.
type MappingEngine struct {
	config        *Config
	translator    definition.Translator
	mappings      map[string]builtinMapping
	mappingSource string
	mutex         sync.RWMutex
	initialized   bool
}

type builtinMapping struct {
	BAP string `yaml:"bapMappings"`
	BPP string `yaml:"bppMappings"`
}

type mappingFile struct {
	Mappings map[string]builtinMapping `yaml:"mappings"`
}

type reqMapperStep struct {
	engine *MappingEngine
	role   string
}

type parsedRequest struct {
	req    map[string]interface{}
	action string
}

// NewReqMapperStep returns a handler step that applies the same reqmapper transformation logic.
// translator must be non-nil.
func NewReqMapperStep(cfg *Config, translator definition.Translator) (definition.Step, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	engine, err := initMappingEngine(cfg, translator)
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
	mappedBody, err := s.transformBody(ctx.Context, ctx.Body)
	if err != nil {
		return err
	}

	ctx.Body = mappedBody
	if ctx.Request != nil {
		ctx.Request.Body = io.NopCloser(bytes.NewReader(mappedBody))
		ctx.Request.ContentLength = int64(len(mappedBody))
		ctx.Request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(mappedBody)), nil
		}
		ctx.Request.TransferEncoding = nil
	}

	return nil
}

func (s *reqMapperStep) transformBody(ctx context.Context, body []byte) ([]byte, error) {
	parsed, err := parseRequestBody(body)
	if err != nil {
		return nil, err
	}

	mappedBody, err := s.engine.Transform(ctx, parsed.action, parsed.req, s.role)
	if err != nil {
		log.Errorf(ctx, err, "Transformation failed for action %s", parsed.action)
		return nil, err
	}

	return mappedBody, nil
}

// parseRequestBody parses the incoming request body and extracts the fields
// the mapping engine needs. Failures are classified onto the Beckn v2.0.0
// ErrorCode taxonomy at the point each cause is known, rather than being
// wrapped in a single generic code by the caller.
func parseRequestBody(body []byte) (*parsedRequest, error) {
	req, reqContext, becknErr := model.ExtractContext(body)
	if becknErr != nil {
		return nil, model.WrapExtractContextErr("failed to decode request body", becknErr)
	}

	action, ok := reqContext["action"].(string)
	if !ok || action == "" {
		return nil, model.NewBadReqErr("SCH_REQUIRED_FIELD_MISSING", errors.New("action field not found or invalid"))
	}

	return &parsedRequest{
		req:    req,
		action: action,
	}, nil
}

// BuildConfig parses the generic plugin config map into a strongly typed reqmapper Config.
func BuildConfig(c map[string]string) *Config {
	cfg := &Config{}
	if role, ok := c["role"]; ok {
		cfg.Role = role
	}
	if mappingsFile, ok := c["mappingsFile"]; ok {
		cfg.MappingsFile = mappingsFile
	}
	return cfg
}

// initMappingEngine initializes a mapping engine for the provided config and translator.
func initMappingEngine(cfg *Config, translator definition.Translator) (*MappingEngine, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}
	if translator == nil {
		return nil, errors.New("translator cannot be nil")
	}

	engine := &MappingEngine{
		config:     cfg,
		translator: translator,
	}

	if err := engine.loadBuiltinMappings(); err != nil {
		return nil, err
	}

	engine.initialized = true
	return engine, nil
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

// loadBuiltinMappings reads every action/direction artifact from the
// configured mappings file. Artifacts aren't compiled here, so a malformed
// one now surfaces at transform time instead of at load.
func (e *MappingEngine) loadBuiltinMappings() error {
	mappings, source, err := e.loadMappingsFromConfig()
	if err != nil {
		return err
	}

	e.mappings = mappings
	e.mappingSource = source

	log.Infof(
		context.Background(),
		"Loaded %d action mapping(s) from %s",
		len(e.mappings),
		source,
	)

	return nil
}

// Transform applies the appropriate mapping based on role and action
func (e *MappingEngine) Transform(ctx context.Context, action string, req map[string]interface{}, role string) ([]byte, error) {
	e.mutex.RLock()
	mapping, found := e.mappings[action]
	e.mutex.RUnlock()

	var artifact string
	switch role {
	case "bap":
		artifact = mapping.BAP
	case "bpp":
		artifact = mapping.BPP
	default:
		return json.Marshal(req)
	}

	// If no mapping found, return original request
	if !found {
		log.Debugf(ctx, "No mapping found for action: %s, role: %s", action, role)
		return json.Marshal(req)
	}

	// Marshal request for the translator. A failure here never reaches the
	// translator, so it's classified separately from Translate errors.
	input, err := json.Marshal(req)
	if err != nil {
		return nil, model.NewBadReqErr(codeBizGenericError, fmt.Errorf("failed to marshal request for mapping: %w", err))
	}

	result, err := e.translator.Translate(ctx, []byte(artifact), input)
	if err != nil {
		return nil, classifyEvaluateErr(err)
	}

	log.Debugf(ctx, "Successfully transformed %s request using %s mapping, %s", action, role, result)
	return result, nil
}

// classifyEvaluateErr maps a Translate failure's v206.JSONataError code, when
// present, onto the Beckn error taxonomy. T*/D* map to
// codeSchemaAdaptationFailed; everything else maps to codeBizGenericError.
func classifyEvaluateErr(err error) *model.CodedErr {
	wrapped := fmt.Errorf("JSONata evaluation failed: %w", err)

	var jsonataErr *v206.JSONataError
	if errors.As(err, &jsonataErr) && len(jsonataErr.Code) > 0 {
		switch jsonataErr.Code[0] {
		case 'T', 'D':
			return model.NewBadReqErr(codeSchemaAdaptationFailed, wrapped)
		}
	}

	return model.NewBadReqErr(codeBizGenericError, wrapped)
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

	actions := make([]string, 0, len(e.mappings))
	for action := range e.mappings {
		actions = append(actions, action)
	}

	return map[string]interface{}{
		"bap_mappings":    actions,
		"bpp_mappings":    actions,
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
