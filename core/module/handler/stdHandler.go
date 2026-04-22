package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
	"github.com/beckn-one/beckn-onix/pkg/response"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	auditlog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// stdHandler orchestrates the execution of defined processing steps.
type stdHandler struct {
	signer           definition.Signer
	steps            []definition.Step
	signValidator    definition.SignValidator
	cache            definition.Cache
	registry         definition.RegistryLookup
	manifestLoader   definition.ManifestLoader
	km               definition.KeyManager
	schemaValidator  definition.SchemaValidator
	policyChecker    definition.PolicyChecker
	router           definition.Router
	publisher        definition.Publisher
	transportWrapper definition.TransportWrapper
	SubscriberID     string
	role             model.Role
	httpClient       *http.Client
	moduleName       string
}

// newHTTPClient creates a new HTTP client with a custom transport configuration.
func newHTTPClient(cfg *HttpClientConfig, wrapper definition.TransportWrapper) *http.Client {
	// Clone the default transport to inherit its sensible defaults.
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Only override the defaults if a value is explicitly provided in the config.
	// A zero value in the config means we stick with the default values.
	if cfg.MaxIdleConns > 0 {
		transport.MaxIdleConns = cfg.MaxIdleConns
	}
	if cfg.MaxIdleConnsPerHost > 0 {
		transport.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
	}
	if cfg.IdleConnTimeout > 0 {
		transport.IdleConnTimeout = cfg.IdleConnTimeout
	}
	if cfg.ResponseHeaderTimeout > 0 {
		transport.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	}

	var finalTransport http.RoundTripper = transport
	if wrapper != nil {
		log.Debugf(context.Background(), "Applying custom transport wrapper")
		finalTransport = wrapper.Wrap(transport)
	}
	return &http.Client{Transport: finalTransport}
}

// NewStdHandler initializes a new processor with plugins and steps.
func NewStdHandler(ctx context.Context, mgr PluginManager, cfg *Config, moduleName string) (http.Handler, error) {
	h := &stdHandler{
		steps:        []definition.Step{},
		SubscriberID: cfg.SubscriberID,
		role:         cfg.Role,
		moduleName:   moduleName,
	}
	// Initialize plugins.
	if err := h.initPlugins(ctx, mgr, &cfg.Plugins); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	// Initialize HTTP client after plugins so transport wrapper can be applied.
	h.httpClient = newHTTPClient(&cfg.HttpClientConfig, h.transportWrapper)
	// Initialize steps.
	if err := h.initSteps(ctx, mgr, cfg); err != nil {
		return nil, fmt.Errorf("failed to initialize steps: %w", err)
	}
	return h, nil
}

// ServeHTTP processes an incoming HTTP request and executes defined processing steps.
func (h *stdHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Header.Set("X-Module-Name", h.moduleName)
	r.Header.Set("X-Role", string(h.role))

	// These headers are only needed for internal instrumentation; avoid leaking them downstream.
	// Use defer to ensure cleanup regardless of return path.
	defer func() {
		r.Header.Del("X-Module-Name")
		r.Header.Del("X-Role")
	}()

	// to start a new trace
	propagator := otel.GetTextMapPropagator()
	traceCtx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	spanName := r.URL.Path
	traceCtx, span := tracer.Start(traceCtx, spanName, trace.WithSpanKind(trace.SpanKindServer))

	//to build the request with trace
	r = r.WithContext(traceCtx)

	var recordOnce func()
	wrapped := &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		record:         nil,
	}

	senderID, receiverID := h.resolveDirection(r.Context())
	httpMeter, _ := GetHTTPMetrics(r.Context())
	if httpMeter != nil {
		recordOnce = func() {
			RecordHTTPRequest(r.Context(), wrapped.statusCode, r.URL.Path, string(h.role), senderID, receiverID)
		}
		wrapped.record = recordOnce
	}

	// set beckn attribute
	setBecknAttr(span, r, h)

	stepCtx, err := h.stepCtx(r, w.Header())
	if err != nil {
		log.Errorf(r.Context(), err, "stepCtx(r):%v", err)
		response.SendNack(r.Context(), wrapped, err)
		return
	}
	log.Request(r.Context(), r, stepCtx.Body)

	defer func() {
		span.SetAttributes(attribute.Int("http.response.status_code", wrapped.statusCode), attribute.String("observedTimeUnixNano", strconv.FormatInt(time.Now().UnixNano(), 10)))
		if wrapped.statusCode < 200 || wrapped.statusCode >= 400 {
			span.SetStatus(codes.Error, "status code is invalid")
		}

		body := stepCtx.Body
		telemetry.EmitAuditLogs(r.Context(), body, auditlog.Int("http.response.status_code", wrapped.statusCode), auditlog.String("http.response.error", errString(err)))
		span.End()
	}()

	// Execute processing steps.
	for _, step := range h.steps {
		if err := step.Run(stepCtx); err != nil {
			log.Errorf(stepCtx, err, "%T.run():%v", step, err)
			response.SendNack(stepCtx, wrapped, err)
			return
		}
	}
	// Restore request body before forwarding or publishing.
	r.Body = io.NopCloser(bytes.NewReader(stepCtx.Body))
	if stepCtx.Route == nil {
		response.SendAck(wrapped)
		return
	}

	// Handle routing based on the defined route type.
	route(stepCtx, r, wrapped, h.publisher, h.httpClient)
}

// stepCtx creates a new StepContext for processing an HTTP request.
func (h *stdHandler) stepCtx(r *http.Request, rh http.Header) (*model.StepContext, error) {
	var bodyBuffer bytes.Buffer
	if _, err := io.Copy(&bodyBuffer, r.Body); err != nil {
		return nil, model.NewBadReqErr(err)
	}
	r.Body.Close()
	subID := h.subID(r.Context())
	return &model.StepContext{
		Context:    r.Context(),
		Request:    r,
		Body:       bodyBuffer.Bytes(),
		Role:       h.role,
		SubID:      subID,
		RespHeader: rh,
	}, nil
}

// subID retrieves the subscriber ID from the request context.
func (h *stdHandler) subID(ctx context.Context) string {
	rSubID, ok := ctx.Value(model.ContextKeySubscriberID).(string)
	if ok {
		return rSubID
	}
	return h.SubscriberID
}

var proxyFunc = proxy

// route handles request forwarding or message publishing based on the routing type.
func route(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, pb definition.Publisher, httpClient *http.Client) {
	log.Debugf(ctx, "Routing to ctx.Route to %#v", ctx.Route)
	switch ctx.Route.TargetType {
	case "url":
		log.Infof(ctx.Context, "Forwarding request to URL: %s", ctx.Route.URL)
		proxyFunc(ctx, r, w, httpClient)
		return
	case "publisher":
		if pb == nil {
			err := fmt.Errorf("publisher plugin not configured")
			log.Errorf(ctx.Context, err, "Invalid configuration:%v", err)
			response.SendNack(ctx, w, err)
			return
		}
		log.Infof(ctx.Context, "Publishing message to: %s", ctx.Route.PublisherID)
		if err := pb.Publish(ctx, ctx.Route.PublisherID, ctx.Body); err != nil {
			log.Errorf(ctx.Context, err, "Failed to publish message")
			http.Error(w, "Error publishing message", http.StatusInternalServerError)
			response.SendNack(ctx, w, err)
			return
		}
	default:
		err := fmt.Errorf("unknown route type: %s", ctx.Route.TargetType)
		log.Errorf(ctx.Context, err, "Invalid configuration:%v", err)
		response.SendNack(ctx, w, err)
		return
	}
	response.SendAck(w)
}
func proxy(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, httpClient *http.Client) {
	target := ctx.Route.URL
	r.Header.Set("X-Forwarded-Host", r.Host)

	director := func(req *http.Request) {
		req.URL = target
		req.Host = target.Host

		log.Request(req.Context(), req, ctx.Body)
	}

	proxy := &httputil.ReverseProxy{
		Director:  director,
		Transport: httpClient.Transport,
	}

	proxy.ServeHTTP(w, r)
}

// loadPlugin is a generic function to load and validate plugins.
func loadPlugin[T any](ctx context.Context, name string, cfg *plugin.Config, mgrFunc func(context.Context, *plugin.Config) (T, error)) (T, error) {
	var zero T
	if cfg == nil {
		log.Debugf(ctx, "Skipping %s plugin: not configured", name)
		return zero, nil
	}

	plugin, err := mgrFunc(ctx, cfg)
	if err != nil {
		return zero, fmt.Errorf("failed to load %s plugin (%s): %w", name, cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded %s plugin: %s", name, cfg.ID)
	return plugin, nil
}

// loadKeyManager loads the KeyManager plugin using the provided PluginManager, cache, and registry.
func loadKeyManager(ctx context.Context, mgr PluginManager, cache definition.Cache, registry definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping KeyManager plugin: not configured")
		return nil, nil
	}
	if cache == nil {
		return nil, fmt.Errorf("failed to load KeyManager plugin (%s): Cache plugin not configured", cfg.ID)
	}
	if registry == nil {
		return nil, fmt.Errorf("failed to load KeyManager plugin (%s): Registry plugin not configured", cfg.ID)
	}
	km, err := mgr.KeyManager(ctx, cache, registry, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load KeyManager plugin (%s): %w", cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded Keymanager plugin: %s", cfg.ID)
	return km, nil
}

func loadManifestLoader(ctx context.Context, mgr PluginManager, cache definition.Cache, registry definition.RegistryLookup, cfg *plugin.Config) (definition.ManifestLoader, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping ManifestLoader plugin: not configured")
		return nil, nil
	}
	if cache == nil {
		return nil, fmt.Errorf("failed to load ManifestLoader plugin (%s): Cache plugin not configured", cfg.ID)
	}
	if registry == nil {
		return nil, fmt.Errorf("failed to load ManifestLoader plugin (%s): Registry plugin not configured", cfg.ID)
	}
	metadataLookup, ok := registry.(definition.RegistryMetadataLookup)
	if !ok {
		return nil, fmt.Errorf("failed to load ManifestLoader plugin (%s): Registry plugin does not implement RegistryMetadataLookup", cfg.ID)
	}
	loader, err := mgr.ManifestLoader(ctx, cache, metadataLookup, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load ManifestLoader plugin (%s): %w", cfg.ID, err)
	}
	log.Debugf(ctx, "Loaded ManifestLoader plugin: %s", cfg.ID)
	return loader, nil
}

func loadPolicyChecker(ctx context.Context, mgr PluginManager, manifestLoader definition.ManifestLoader, cfg *plugin.Config) (definition.PolicyChecker, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping PolicyChecker plugin: not configured")
		return nil, nil
	}

	checker, err := mgr.PolicyChecker(ctx, manifestLoader, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load PolicyChecker plugin (%s): %w", cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded PolicyChecker plugin: %s", cfg.ID)
	return checker, nil
}

// initPlugins initializes required plugins for the processor.
func (h *stdHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg) error {
	var err error
	if h.cache, err = loadPlugin(ctx, "Cache", cfg.Cache, mgr.Cache); err != nil {
		return err
	}
	if h.registry, err = loadPlugin(ctx, "Registry", cfg.Registry, mgr.Registry); err != nil {
		return err
	}
	if h.km, err = loadKeyManager(ctx, mgr, h.cache, h.registry, cfg.KeyManager); err != nil {
		return err
	}
	if h.manifestLoader, err = loadManifestLoader(ctx, mgr, h.cache, h.registry, cfg.ManifestLoader); err != nil {
		return err
	}
	if h.signValidator, err = loadPlugin(ctx, "SignValidator", cfg.SignValidator, mgr.SignValidator); err != nil {
		return err
	}
	if h.schemaValidator, err = loadPlugin(ctx, "SchemaValidator", cfg.SchemaValidator, mgr.SchemaValidator); err != nil {
		return err
	}
	if h.router, err = loadPlugin(ctx, "Router", cfg.Router, mgr.Router); err != nil {
		return err
	}
	if h.publisher, err = loadPlugin(ctx, "Publisher", cfg.Publisher, mgr.Publisher); err != nil {
		return err
	}
	if h.signer, err = loadPlugin(ctx, "Signer", cfg.Signer, mgr.Signer); err != nil {
		return err
	}
	if h.transportWrapper, err = loadPlugin(ctx, "TransportWrapper", cfg.TransportWrapper, mgr.TransportWrapper); err != nil {
		return err
	}
	if h.policyChecker, err = loadPolicyChecker(ctx, mgr, h.manifestLoader, cfg.PolicyChecker); err != nil {
		return err
	}

	log.Debugf(ctx, "All required plugins successfully loaded for stdHandler")
	return nil
}

// initSteps initializes and validates processing steps for the processor.
func (h *stdHandler) initSteps(ctx context.Context, mgr PluginManager, cfg *Config) error {
	steps := make(map[string]definition.Step)

	// Load plugin-based steps
	for _, c := range cfg.Plugins.Steps {
		step, err := mgr.Step(ctx, &c)
		if err != nil {
			return fmt.Errorf("failed to initialize plugin step %s: %w", c.ID, err)
		}
		steps[c.ID] = step
	}

	// Register processing steps
	for _, step := range cfg.Steps {
		var s definition.Step
		var err error

		switch step {
		case "sign":
			s, err = newSignStep(h.signer, h.km)
		case "validateSign":
			s, err = newValidateSignStep(h.signValidator, h.km)
		case "validateSchema":
			s, err = newValidateSchemaStep(h.schemaValidator)
		case "addRoute":
			s, err = newAddRouteStep(h.router)
		case "checkPolicy":
			s, err = newCheckPolicyStep(h.policyChecker)
		default:
			if customStep, exists := steps[step]; exists {
				s = customStep
			} else {
				return fmt.Errorf("unrecognized step: %s", step)
			}
		}

		if err != nil {
			return err
		}
		instrumentedStep, wrapErr := NewInstrumentedStep(s, step, h.moduleName)
		if wrapErr != nil {
			log.Warnf(ctx, "Failed to instrument step %s: %v", step, wrapErr)
			h.steps = append(h.steps, s)
			continue
		}
		h.steps = append(h.steps, instrumentedStep)
	}
	log.Infof(ctx, "Processor steps initialized: %v", cfg.Steps)
	return nil
}

func (h *stdHandler) resolveDirection(ctx context.Context) (senderID, receiverID string) {
	selfID := h.SubscriberID
	remoteID, _ := ctx.Value(model.ContextKeyRemoteID).(string)
	if strings.Contains(h.moduleName, "Caller") {
		return selfID, remoteID
	}
	return remoteID, selfID
}

func setBecknAttr(span trace.Span, r *http.Request, h *stdHandler) {
	senderID, receiverID := h.resolveDirection(r.Context())
	attrs := []attribute.KeyValue{
		telemetry.AttrRecipientID.String(receiverID),
		telemetry.AttrSenderID.String(senderID),
		attribute.String("span_uuid", uuid.New().String()),
		attribute.String("http.request.method", r.Method),
		attribute.String("http.route", r.URL.Path),
	}

	if trxID, ok := r.Context().Value(model.ContextKeyTxnID).(string); ok {
		attrs = append(attrs, attribute.String("transaction_id", trxID))
	}
	if mesID, ok := r.Context().Value(model.ContextKeyMsgID).(string); ok {
		attrs = append(attrs, attribute.String("message_id", mesID))
	}
	if parentID, ok := r.Context().Value(model.ContextKeyParentID).(string); ok && parentID != "" {
		attrs = append(attrs, attribute.String("parentSpanId", parentID))
	}
	if r.Host != "" {
		attrs = append(attrs, attribute.String("server.address", r.Host))
	}

	if ua := r.UserAgent(); ua != "" {
		attrs = append(attrs, attribute.String("user_agent.original", ua))
	}

	span.SetAttributes(attrs...)
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
