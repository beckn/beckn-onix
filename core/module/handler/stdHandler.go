package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/beckn/beckn-onix/core/module/client"
	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
	"github.com/beckn/beckn-onix/pkg/response"
)

// stdHandler orchestrates the execution of defined processing steps.
type stdHandler struct {
	signer          definition.Signer
	steps           []definition.Step
	signValidator   definition.SignValidator
	cache           definition.Cache
	km              definition.KeyManager
	schemaValidator definition.SchemaValidator
	router          definition.Router
	publisher       definition.Publisher
	SubscriberID    string
	role            model.Role
}

// NewStdHandler initializes a new processor with plugins and steps.
func NewStdHandler(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error) {
	h := &stdHandler{
		steps:        []definition.Step{},
		SubscriberID: cfg.SubscriberID,
		role:         cfg.Role,
	}
	// Initialize plugins.
	if err := h.initPlugins(ctx, mgr, &cfg.Plugins, cfg.RegistryURL); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	// Initialize steps.
	if err := h.initSteps(ctx, mgr, cfg); err != nil {
		return nil, fmt.Errorf("failed to initialize steps: %w", err)
	}
	return h, nil
}

// ServeHTTP processes an incoming HTTP request and executes defined processing steps.
func (h *stdHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, err := h.stepCtx(r, w.Header())
	if err != nil {
		log.Errorf(r.Context(), err, "stepCtx(r):%v", err)
		response.SendNack(r.Context(), w, err)
		return
	}
	log.Request(r.Context(), r, ctx.Body)

	// Execute processing steps.
	for _, step := range h.steps {
		if err := step.Run(ctx); err != nil {
			log.Errorf(ctx, err, "%T.run(%v):%v", step, ctx, err)
			response.SendNack(ctx, w, err)
			return
		}
	}
	// Restore request body before forwarding or publishing.
	r.Body = io.NopCloser(bytes.NewReader(ctx.Body))
	if ctx.Route == nil {
		response.SendAck(w)
		return
	}

	// Handle routing based on the defined route type.
	route(ctx, r, w, h.publisher)
}

// stepCtx creates a new StepContext for processing an HTTP request.
func (h *stdHandler) stepCtx(r *http.Request, rh http.Header) (*model.StepContext, error) {
	var bodyBuffer bytes.Buffer
	if _, err := io.Copy(&bodyBuffer, r.Body); err != nil {
		return nil, model.NewBadReqErr(err)
	}
	r.Body.Close()
	subID := h.subID(r.Context())
	if len(subID) == 0 {
		return nil, model.NewBadReqErr(fmt.Errorf("subscriberID not set"))
	}
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
	rSubID, ok := ctx.Value("subscriber_id").(string)
	if ok {
		return rSubID
	}
	return h.SubscriberID
}

var proxyFunc = proxy

// route handles request forwarding or message publishing based on the routing type.
func route(ctx *model.StepContext, r *http.Request, w http.ResponseWriter, pb definition.Publisher) {
	log.Debugf(ctx, "Routing to ctx.Route to %#v", ctx.Route)
	switch ctx.Route.TargetType {
	case "url":
		log.Infof(ctx.Context, "Forwarding request to URL: %s", ctx.Route.URL)
		proxyFunc(r, w, ctx.Route.URL)
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

// proxy forwards the request to a target URL using a reverse proxy.
func proxy(r *http.Request, w http.ResponseWriter, target *url.URL) {
	r.URL.Scheme = target.Scheme
	r.URL.Host = target.Host
	r.URL.Path = target.Path

	r.Header.Set("X-Forwarded-Host", r.Host)
	proxy := httputil.NewSingleHostReverseProxy(target)
	log.Infof(r.Context(), "Proxying request to: %s", target)

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

// loadKeyManager loads the KeyManager plugin using the provided PluginManager, cache, and registry URL.
func loadKeyManager(ctx context.Context, mgr PluginManager, cache definition.Cache, cfg *plugin.Config, regURL string) (definition.KeyManager, error) {
	if cfg == nil {
		log.Debug(ctx, "Skipping KeyManager plugin: not configured")
		return nil, nil
	}
	if cache == nil {
		return nil, fmt.Errorf("failed to load KeyManager plugin (%s): Cache plugin not configured", cfg.ID)
	}
	rClient := client.NewRegisteryClient(&client.Config{RegisteryURL: regURL})
	km, err := mgr.KeyManager(ctx, cache, rClient, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load cache plugin (%s): %w", cfg.ID, err)
	}

	log.Debugf(ctx, "Loaded Keymanager plugin: %s", cfg.ID)
	return km, nil
}

// initPlugins initializes required plugins for the processor.
func (h *stdHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg, regURL string) error {
	var err error
	if h.cache, err = loadPlugin(ctx, "Cache", cfg.Cache, mgr.Cache); err != nil {
		return err
	}
	if h.km, err = loadKeyManager(ctx, mgr, h.cache, cfg.KeyManager, regURL); err != nil {
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
		h.steps = append(h.steps, s)
	}
	log.Infof(ctx, "Processor steps initialized: %v", cfg.Steps)
	return nil
}
