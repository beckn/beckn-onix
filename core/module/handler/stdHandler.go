package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
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
	signer             definition.Signer
	steps              []definition.Step
	responseSteps      []definition.ResponseStep
	signValidator      definition.SignValidator
	cache              definition.Cache
	registry           definition.RegistryLookup
	manifestLoader     definition.ManifestLoader
	km                 definition.KeyManager
	schemaValidator    definition.SchemaValidator
	policyChecker         definition.PolicyChecker
	schemaVersionMediator definition.SchemaVersionMediator
	router                definition.Router
	publisher          definition.Publisher
	transportWrapper   definition.TransportWrapper
	payloadTransformer definition.Step
	payloadStore       definition.PayloadStore
	// ackSigner is non-nil only when the "signAck" step is configured (Receiver
	// modules). It is also used to sign pipeline-NACK responses so that ALL
	// synchronous responses carry a Signature header per NFH-007 CON-004-02.
	ackSigner    *ackSignerStep
	SubscriberID string
	role         model.Role
	basePath     string
	httpClient   *http.Client
	moduleName   string
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
		steps:         []definition.Step{},
		responseSteps: []definition.ResponseStep{},
		SubscriberID:  cfg.SubscriberID,
		role:          cfg.Role,
		basePath:      cfg.BasePath,
		moduleName:    moduleName,
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

	// Read body early to extract the Beckn action, which is needed for both
	// the trace context span attributes and for the HTTP request metrics.
	// This must happen before span creation so the attributes are available.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Errorf(r.Context(), err, "failed to read request body: %v", err)
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	action := extractBecknAction(body)
	if action == "" {
		action = r.URL.Path
	}

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
			RecordHTTPRequest(r.Context(), wrapped.statusCode, action, string(h.role), senderID, receiverID)
		}
		wrapped.record = recordOnce
	}

	// set beckn attribute
	setBecknAttr(span, r, h, action)

	stepCtx, err := h.stepCtx(r, w.Header())
	if err != nil {
		log.Errorf(r.Context(), err, "stepCtx(r):%v", err)
		sendNack(r.Context(), wrapped, err)
		return
	}
	log.Request(r.Context(), r, stepCtx.Body)

	var responseBody []byte

	defer func() {
		span.SetAttributes(attribute.Int("http.response.status_code", wrapped.statusCode), attribute.String("http.request.error", errString(err)), attribute.String("observedTimeUnixNano", strconv.FormatInt(time.Now().UnixNano(), 10)))
		if wrapped.statusCode < 200 || wrapped.statusCode >= 400 {
			span.SetStatus(codes.Error, "status code is invalid")
		}

		body := stepCtx.Body
		telemetry.EmitAuditLogs(r.Context(), body, r.Header, auditlog.String("audit.direction", "request"), auditlog.Int("http.response.status_code", wrapped.statusCode), auditlog.String("http.request.error", errString(err)), auditlog.String("sender.id", senderID), auditlog.String("receiver.id", receiverID))
		if len(responseBody) > 0 {
			telemetry.EmitAuditLogs(r.Context(), responseBody, nil, auditlog.String("audit.direction", "response"), auditlog.Int("http.response.status_code", wrapped.statusCode), auditlog.String("http.request.error", errString(err)), auditlog.String("sender.id", senderID), auditlog.String("receiver.id", receiverID))
		}
		span.End()
	}()

	// Execute processing steps.
	var pipelineErr error
	for _, step := range h.steps {
		if pipelineErr = step.Run(stepCtx); pipelineErr != nil {
			log.Errorf(stepCtx, pipelineErr, "%T.run():%v", step, pipelineErr)
			// Sign the NACK before writing HTTP headers (NFH-007 CON-004-02).
			h.signNackResponse(stepCtx, pipelineErr)
			responseBody = sendNack(stepCtx, wrapped, pipelineErr)
			break
		}
	}

	// Send ACK / route on success.
	if pipelineErr == nil {
		// Restore request body and metadata before forwarding or publishing.
		syncRequestBody(r, stepCtx.Body)
		if stepCtx.Route == nil {
			// No routing — ONIX writes the ACK directly. Run response steps here
			// with resp=nil (publisher path semantics).
			for _, step := range h.responseSteps {
				if err = step.RunOnResponse(stepCtx, nil); err != nil {
					log.Errorf(stepCtx, err, "%T.RunOnResponse():%v", step, err)
					// ackSignerStep itself failed — sign the NACK body if
					// a different signing mechanism is available, or send unsigned.
					h.signNackResponse(stepCtx, err)
					responseBody = sendNack(stepCtx, wrapped, err)
					return
				}
			}
			responseBody = sendAck(stepCtx, wrapped)
			return
		}
		// Handle routing based on the defined route type.
		route(stepCtx, r, wrapped, h.publisher, h.httpClient, h.responseSteps, h.signNackResponse, &responseBody)
	}
}

// signNackResponse signs the NACK response body and sets the Signature header
// on ctx.RespHeader before sendNack writes the HTTP headers to the
// wire. This satisfies NFH-007 CON-004-02 for ONIX-generated error responses.
//
// Signing is a best-effort operation: if it fails (e.g. key manager unavailable)
// the NACK is sent unsigned with a warning log rather than blocking the error
// path. The nil-ackSigner / pre-v2 / empty-subID fast paths are handled inside
// ackSignerStep.signBodyAndSetHeader.
func (h *stdHandler) signNackResponse(ctx *model.StepContext, err error) {
	if h.ackSigner == nil {
		return
	}
	if !model.IsAtLeastV2(ctx.ProtocolVersion) {
		return
	}
	if len(ctx.SubID) == 0 {
		return
	}
	nackBody := nackBytes(ctx.Context, err)
	if serr := h.ackSigner.signBodyAndSetHeader(ctx, nackBody); serr != nil {
		log.Warnf(ctx, "signNackResponse: failed to sign NACK — sending unsigned: %v", serr)
	}
}

// stepCtx creates a new StepContext for processing an HTTP request.
func (h *stdHandler) stepCtx(r *http.Request, rh http.Header) (*model.StepContext, error) {
	var bodyBuffer bytes.Buffer
	if _, err := io.Copy(&bodyBuffer, r.Body); err != nil {
		return nil, model.NewBadReqErr(err)
	}
	r.Body.Close()
	body := bodyBuffer.Bytes()
	subID := h.subID(r.Context())
	protocolVersion := extractProtocolVersion(body)
	messageID := extractMessageID(body)
	log.Debugf(r.Context(), "stepCtx: extracted protocolVersion=%q messageId=%q", protocolVersion, messageID)
	inboundAuthSignature := extractAuthSignature(r.Header.Get(model.AuthHeaderSubscriber))
	remoteKeyID := extractRemoteKeyID(r.Header.Get(model.AuthHeaderSubscriber))
	// Store both protocol version and message ID in the Go context so downstream
	// functions that only receive a context.Context (e.g. sendNack,
	// sendAck) can read them without needing StepContext.
	ctx := context.WithValue(r.Context(), model.ContextKeyProtocolVersion, protocolVersion)
	ctx = context.WithValue(ctx, model.ContextKeyMsgID, messageID)
	return &model.StepContext{
		Context:              ctx,
		Request:              r,
		Body:                 body,
		Role:                 h.role,
		SubID:                subID,
		RespHeader:           rh,
		ProtocolVersion:      protocolVersion,
		MessageID:            messageID,
		InboundAuthSignature: inboundAuthSignature,
		RemoteKeyID:          remoteKeyID,
		IsCallerHandler:      strings.Contains(h.moduleName, "Caller"),
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

var proxyFunc = func(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, httpClient *http.Client, responseSteps []definition.ResponseStep, responseBody *[]byte) {
	proxy(ctx, r, w, httpClient, responseSteps, responseBody)
}

// nackSignerFunc is the function type used to sign NACK responses before they
// are written to the wire. On Receiver modules h.signNackResponse is passed;
// on Caller modules (no ackSigner) the function is a no-op.
type nackSignerFunc func(ctx *model.StepContext, err error)

// route handles request forwarding or message publishing based on the routing type.
func route(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, pb definition.Publisher, httpClient *http.Client, responseSteps []definition.ResponseStep, signNack nackSignerFunc, responseBody *[]byte) {
	log.Debugf(ctx, "Routing to ctx.Route to %#v", ctx.Route)
	switch ctx.Route.TargetType {
	case "url":
		log.Infof(ctx.Context, "Forwarding request to URL: %s", ctx.Route.URL)
		proxyFunc(ctx, r, w, httpClient, responseSteps, responseBody)
		return
	case "publisher":
		if pb == nil {
			err := fmt.Errorf("publisher plugin not configured")
			log.Errorf(ctx.Context, err, "Invalid configuration:%v", err)
			signNack(ctx, err)
			*responseBody = sendNack(ctx, w, err)
			return
		}
		log.Infof(ctx.Context, "Publishing message to: %s", ctx.Route.PublisherID)
		if err := pb.Publish(ctx, ctx.Route.PublisherID, ctx.Body); err != nil {
			log.Errorf(ctx.Context, err, "Failed to publish message")
			signNack(ctx, err)
			*responseBody = sendNack(ctx, w, err)
			return
		}
		// Publisher path: ONIX writes the ACK. Run response steps with resp=nil.
		for _, step := range responseSteps {
			if err := step.RunOnResponse(ctx, nil); err != nil {
				log.Errorf(ctx.Context, err, "response step failed: %v", err)
				signNack(ctx, err)
				*responseBody = sendNack(ctx, w, err)
				return
			}
		}
	default:
		err := fmt.Errorf("unknown route type: %s", ctx.Route.TargetType)
		log.Errorf(ctx.Context, err, "Invalid configuration:%v", err)
		signNack(ctx, err)
		*responseBody = sendNack(ctx, w, err)
		return
	}
	*responseBody = sendAck(ctx, w)
}

// proxyBufPool backs reverseProxyBufferPool. Without a BufferPool set on
// httputil.ReverseProxy, copyBuffer allocates a fresh 32KB []byte per
// proxied response (see https://github.com/beckn/beckn-onix/issues/848) —
// pooling avoids that allocation on every request.
var proxyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

// reverseProxyBufferPool implements httputil.BufferPool over proxyBufPool.
// Buffers are stored as *[]byte rather than []byte so Put does not box a
// fresh interface value (and allocate) on every call.
type reverseProxyBufferPool struct{}

func (reverseProxyBufferPool) Get() []byte {
	return *(proxyBufPool.Get().(*[]byte))
}

func (reverseProxyBufferPool) Put(b []byte) {
	proxyBufPool.Put(&b)
}

func proxy(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, httpClient *http.Client, responseSteps []definition.ResponseStep, responseBody *[]byte) {
	target := ctx.Route.URL
	r.Header.Set("X-Forwarded-Host", r.Host)

	director := func(req *http.Request) {
		req.URL = target
		req.Host = target.Host

		log.Request(req.Context(), req, ctx.Body)
	}

	// modifyResponse pre-reads the upstream response body once, constructs a
	// ResponseStepContext, then runs all response steps. Body restoration for
	// ReverseProxy happens here — individual steps read from rctx.Body and do
	// not need to touch resp.Body directly.
	modifyResponse := func(resp *http.Response) error {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("modifyResponse: failed to read upstream response body: %w", err)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		rctx := &model.ResponseStepContext{
			StatusCode: resp.StatusCode,
			Header:     resp.Header,
			Body:       body,
		}
		for _, step := range responseSteps {
			if err := step.RunOnResponse(ctx, rctx); err != nil {
				return err
			}
		}
		// Capture only after all response steps succeed — if a step fails the
		// ReverseProxy error handler writes a 502, so the upstream body is not
		// what the caller received.
		*responseBody = body
		return nil
	}

	p := &httputil.ReverseProxy{
		Director:       director,
		Transport:      httpClient.Transport,
		ModifyResponse: modifyResponse,
		BufferPool:     reverseProxyBufferPool{},
	}

	p.ServeHTTP(w, r)
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

// loadKeyManager loads the KeyManager plugin using the provided PluginManager and registry.
func loadKeyManager(ctx context.Context, mgr PluginManager, registry definition.RegistryLookup, cfg *plugin.Config) (definition.KeyManager, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping KeyManager plugin: not configured")
		return nil, nil
	}
	if registry == nil {
		return nil, fmt.Errorf("failed to load KeyManager plugin (%s): Registry plugin not configured", cfg.ID)
	}
	km, err := mgr.KeyManager(ctx, registry, cfg)
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

func loadPayloadStore(ctx context.Context, mgr PluginManager, cache definition.Cache, namespace string, cfg *plugin.Config, role model.Role) (definition.PayloadStore, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping PayloadStore plugin: not configured")
		return nil, nil
	}
	if cache == nil {
		return nil, fmt.Errorf("failed to load PayloadStore plugin (%s): Cache plugin not configured", cfg.ID)
	}
	if role == model.RoleBAP && cfg.Config["storeSignature"] == "false" {
		log.Warnf(ctx, "PayloadStore plugin (%s): storeSignature is disabled for a BAP handler — Authorization header will not be stored", cfg.ID)
	}
	ps, err := mgr.PayloadStore(ctx, cache, namespace, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load PayloadStore plugin (%s): %w", cfg.ID, err)
	}
	log.Debugf(ctx, "Loaded PayloadStore plugin: %s", cfg.ID)
	return ps, nil
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

func loadSchemaVersionMediator(ctx context.Context, mgr PluginManager, manifestLoader definition.ManifestLoader, cfg *plugin.Config) (definition.SchemaVersionMediator, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping SchemaVersionMediator plugin: not configured")
		return nil, nil
	}
	if manifestLoader == nil {
		return nil, fmt.Errorf("failed to load SchemaVersionMediator plugin (%s): ManifestLoader plugin not configured", cfg.ID)
	}
	mediator, err := mgr.SchemaVersionMediator(ctx, manifestLoader, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load SchemaVersionMediator plugin (%s): %w", cfg.ID, err)
	}
	log.Debugf(ctx, "Loaded SchemaVersionMediator plugin: %s", cfg.ID)
	return mediator, nil
}

func loadPayloadTransformerStep(ctx context.Context, mgr PluginManager, cfg *plugin.Config) (definition.Step, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping PayloadTransformer plugin: not configured")
		return nil, nil
	}

	step, err := mgr.Step(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load PayloadTransformer plugin (%s): %w", cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded PayloadTransformer plugin: %s", cfg.ID)
	return step, nil
}

// initPlugins initializes required plugins for the processor.
func (h *stdHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg) error {
	var err error
	if h.cache, err = loadPlugin(ctx, "Cache", cfg.Cache, mgr.Cache); err != nil {
		return err
	}
	if h.registry, err = loadPlugin(ctx, "Registry", cfg.Registry, func(ctx context.Context, cfg *plugin.Config) (definition.RegistryLookup, error) {
		return mgr.Registry(ctx, h.cache, cfg)
	}); err != nil {
		return err
	}
	if h.km, err = loadKeyManager(ctx, mgr, h.registry, cfg.KeyManager); err != nil {
		return err
	}
	if h.manifestLoader, err = loadManifestLoader(ctx, mgr, h.cache, h.registry, cfg.ManifestLoader); err != nil {
		return err
	}
	if h.payloadStore, err = loadPayloadStore(ctx, mgr, h.cache, "onix", cfg.PayloadStore, h.role); err != nil {
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
	if h.schemaVersionMediator, err = loadSchemaVersionMediator(ctx, mgr, h.manifestLoader, cfg.SchemaVersionMediator); err != nil {
		return err
	}
	if h.payloadTransformer, err = loadPayloadTransformerStep(ctx, mgr, cfg.PayloadTransformer); err != nil {
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
		case "signAck":
			// signAck is a ResponseStep — appended to responseSteps, not steps.
			// Also stored as h.ackSigner so ServeHTTP can sign pipeline NACKs.
			as, rsErr := newAckSignerStep(h.signer, h.km)
			if rsErr != nil {
				return rsErr
			}
			// newAckSignerStep returns definition.ResponseStep; assert to *ackSignerStep
			// so signBodyAndSetHeader is accessible for NACK signing.
			if concreteAS, ok := as.(*ackSignerStep); ok {
				h.ackSigner = concreteAS
			}
			instrumentedAS, wrapErr := NewInstrumentedResponseStep(as, step, h.moduleName)
			if wrapErr != nil {
				log.Warnf(ctx, "Failed to instrument response step %s: %v", step, wrapErr)
				h.responseSteps = append(h.responseSteps, as)
				continue
			}
			h.responseSteps = append(h.responseSteps, instrumentedAS)
			continue
		case "validateAckSign":
			// validateAckSign is a ResponseStep — verifies the Signature header
			// on the ACK received by a Caller handler (NFH-004 §3.4).
			rs, rsErr := newValidateAckSignatureStep(h.signValidator, h.km)
			if rsErr != nil {
				return rsErr
			}
			instrumentedRS, wrapErr := NewInstrumentedResponseStep(rs, step, h.moduleName)
			if wrapErr != nil {
				log.Warnf(ctx, "Failed to instrument response step %s: %v", step, wrapErr)
				h.responseSteps = append(h.responseSteps, rs)
				continue
			}
			h.responseSteps = append(h.responseSteps, instrumentedRS)
			continue
		case "sign":
			s, err = newSignStep(h.signer, h.km, h.payloadStore)
		case "validateSign":
			s, err = newValidateSignStep(h.signValidator, h.km, h.payloadStore)
		case "validateSchema":
			s, err = newValidateSchemaStep(h.schemaValidator, h.basePath)
		case "addRoute":
			s, err = newAddRouteStep(h.router, h.basePath)
		case "checkPolicy":
			s, err = newCheckPolicyStep(h.policyChecker)
		case "mediateSchema":
			s, err = newMediateSchemaStep(h.schemaVersionMediator)
		case "transformPayload":
			if h.payloadTransformer == nil {
				return fmt.Errorf("invalid config: PayloadTransformer plugin not configured")
			}
			s = h.payloadTransformer
		case "storePayload":
			s, err = newStorePayloadStep(h.payloadStore)
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

func syncRequestBody(r *http.Request, body []byte) {
	if r == nil {
		return
	}

	reader := bytes.NewReader(body)
	r.Body = io.NopCloser(reader)
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	r.TransferEncoding = nil
}

func (h *stdHandler) resolveDirection(ctx context.Context) (senderID, receiverID string) {
	selfID := h.SubscriberID
	remoteID, _ := ctx.Value(model.ContextKeyRemoteID).(string)
	if strings.Contains(h.moduleName, "Caller") {
		return selfID, remoteID
	}
	return remoteID, selfID
}

func setBecknAttr(span trace.Span, r *http.Request, h *stdHandler, action string) {
	senderID, receiverID := h.resolveDirection(r.Context())
	attrs := []attribute.KeyValue{
		telemetry.AttrRecipientID.String(receiverID),
		telemetry.AttrSenderID.String(senderID),
		attribute.String("span_uuid", uuid.New().String()),
		attribute.String("http.request.method", r.Method),
		attribute.String("http.route", r.URL.Path),
		telemetry.AttrAction.String(action),
	}

	if trxID, ok := r.Context().Value(model.ContextKeyTxnID).(string); ok {
		attrs = append(attrs, attribute.String("transaction_id", trxID))
	}
	if mesID, ok := r.Context().Value(model.ContextKeyMsgID).(string); ok {
		attrs = append(attrs, attribute.String("message_id", mesID))
	}
	if parentID, ok := r.Context().Value(model.ContextKeyParentID).(string); ok && parentID != "" {
		// Attribute name matches audit.go ("parent_id") and the context key name so that
		// cross-signal queries in Loki/Jaeger can join on a single consistent key.
		// Previously named "parentSpanId" (camelCase), which was inconsistent and
		// misleadingly implied an OTel span-parentage relationship; the value is the
		// Beckn network identity of this adapter (role:subscriberID:pod), not a span ID.
		attrs = append(attrs, attribute.String("parent_id", parentID))
	}
	if r.Host != "" {
		attrs = append(attrs, attribute.String("server.address", r.Host))
	}

	if ua := r.UserAgent(); ua != "" {
		attrs = append(attrs, attribute.String("user_agent.original", ua))
	}

	span.SetAttributes(attrs...)
}

func extractBecknAction(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		Context struct {
			Action string `json:"action"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Context.Action
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
