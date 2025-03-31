package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/beckn/beckn-onix/core/module/client"
	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

type registryClient interface {
	Subscribe(ctx context.Context, subscription *model.Subscription) error
	Lookup(ctx context.Context, subscription *model.Subscription) ([]model.Subscription, error)
}

// regSubscibeHandler encapsulates the subscription logic.
type npSubscibeHandler struct {
	km      definition.KeyManager
	rClient registryClient
}

// NewRegSubscibeHandler creates a new instance of SubscriptionService.
func NewNPSubscibeHandler(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error) {
	s := &npSubscibeHandler{
		rClient: client.NewRegisteryClient(&client.Config{RegisteryURL: cfg.RegistryURL}),
	}
	// Initialize plugins
	if err := s.initPlugins(ctx, mgr, &cfg.Plugins); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}

	return s, nil
}

// initPlugins initializes required plugins for the processor.
func (h *npSubscibeHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg) error {
	var err error
	if cfg.Cache == nil {
		return fmt.Errorf("invalid config: Cache missing")
	}
	cache, err := mgr.Cache(ctx, cfg.Cache)
	if err != nil {
		return fmt.Errorf("failed to load cache: %w", err)
	}
	if cfg.KeyManager == nil {
		return fmt.Errorf("invalid config: KeyManager missing")
	}
	if h.km, err = mgr.KeyManager(ctx, cache, h.rClient, cfg.KeyManager); err != nil {
		return fmt.Errorf("failed to load cache: %w", err)
	}
	return nil
}

// ServeHTTP handles incoming subscription requests.
func (h *npSubscibeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Debug(r.Context(), "NP Subscribe handler called.")
	log.Request(r.Context(), r, nil)
	// Ensure the request method is POST
	if r.Method != http.MethodPost {
		http.Error(w, "invalid request method, only POST allowed", http.StatusMethodNotAllowed)
		return
	}
	// Parse request body
	var reqPayload model.Subscription
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate subscriber_id
	if reqPayload.SubscriberID == "" {
		http.Error(w, "missing subscriber_id", http.StatusBadRequest)
		return
	}
	// Validate subscriber_id
	if reqPayload.URL == "" {
		http.Error(w, "missing subscriber url", http.StatusBadRequest)
		return
	}
	keys, err := h.km.GenerateKeyPairs()
	if err != nil {
		log.Errorf(r.Context(), err, "failed to generate keys")
		http.Error(w, "failed to generate keys", http.StatusInternalServerError)
		return
	}
	log.Debugf(r.Context(), "got keys %#v", keys)
	// Create subscription request
	reqData := &model.Subscription{
		KeyID:            keys.UniqueKeyID,
		SigningPublicKey: keys.SigningPublic,
		EncrPublicKey:    keys.EncrPublic,
		Subscriber:       reqPayload.Subscriber,
	}

	if err := h.rClient.Subscribe(r.Context(), reqData); err != nil {
		log.Errorf(r.Context(), err, "Call to registery failed")
		http.Error(w, "failed to send request", http.StatusInternalServerError)
		return
	}
	if err := h.km.StorePrivateKeys(r.Context(), reqPayload.SubscriberID, keys); err != nil {
		log.Errorf(r.Context(), err, "StorePrivateKeys failed")
		http.Error(w, "failed to StorePrivateKeys", http.StatusInternalServerError)
		return
	}
	// Forward the response back to the client
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Successful"))
}
