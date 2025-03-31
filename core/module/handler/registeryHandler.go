package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/beckn/beckn-onix/pkg/plugin/definition"
)

// regSubscibeHandler encapsulates the subscription logic.
type regSubscibeHandler struct {
	cache definition.Cache
}

// NewRegSubscibeHandler creates a new instance of SubscriptionService.
func NewRegSubscibeHandler(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error) {
	s := &regSubscibeHandler{}
	// Initialize plugins
	if err := s.initPlugins(ctx, mgr, &cfg.Plugins); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	return s, nil
}

// initPlugins initializes required plugins for the processor.
func (p *regSubscibeHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg) error {
	var err error
	if cfg.Cache == nil {
		return fmt.Errorf("invalid config: Cache missing")
	}
	if p.cache, err = mgr.Cache(ctx, cfg.Cache); err != nil {
		return fmt.Errorf("failed to load cache: %w", err)
	}
	return nil
}

// SubscribeHandler processes subscription requests.
func (s *regSubscibeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Debug(r.Context(), "Reg Subscribe handler called.")
	// Parse request body
	var req model.Subscription
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Errorf(r.Context(), err, "Reg Subscribe handler: Bad Request")
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// Validate the request
	if err := validateSubscriptionReq(&req); err != nil {
		log.Errorf(r.Context(), err, "Reg Subscribe handler: Bad Request")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Process subscription
	if err := s.subscribe(&req); err != nil {
		log.Errorf(r.Context(), err, "failed to process subscription")
		http.Error(w, "failed to process subscription", http.StatusInternalServerError)
		return
	}

	// Respond with success
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Subscription successful"))
}

// validate checks if all required fields are present and valid.
func validateSubscriptionReq(req *model.Subscription) error {
	if req == nil {
		return errors.New("missing request")
	}
	if req.SigningPublicKey == "" {
		return errors.New("missing signing public key")
	}
	if req.EncrPublicKey == "" {
		return errors.New("missing encryption public key")
	}
	if req.URL == "" {
		return errors.New("missing URL")
	}
	return nil
}

// subscribe processes the subscription logic and stores it in the cache.
func (s *regSubscibeHandler) subscribe(req *model.Subscription) error {
	subscription := &model.Subscription{
		Subscriber:       req.Subscriber,
		SigningPublicKey: req.SigningPublicKey,
		EncrPublicKey:    req.EncrPublicKey,
		KeyID:            req.KeyID,
		Status:           "UNDER_SUBSCRIPTION",
		ValidFrom:        time.Now(),
		ValidUntil:       time.Now().Add(48 * time.Hour),
		Created:          time.Now(),
		Updated:          time.Now(),
	}

	// Store in cache
	cacheKey := fmt.Sprintf("subscriber:%s", req.SubscriberID)
	subscriptionData, err := json.Marshal(subscription)
	if err != nil {
		return fmt.Errorf("failed to marshal subscription data: %w", err)
	}
	return s.cache.Set(context.Background(), cacheKey, string(subscriptionData), 240*time.Hour) // Default 24hr TTL
}

// lookUpHandler encapsulates the lookup logic.
type lookUpHandler struct {
	cache definition.Cache
}

// NewLookHandler creates a new instance of RegistryHandler.
func NewLookHandler(ctx context.Context, mgr PluginManager, cfg *Config) (http.Handler, error) {
	h := &lookUpHandler{}
	if err := h.initPlugins(ctx, mgr, &cfg.Plugins); err != nil {
		return nil, fmt.Errorf("failed to initialize plugins: %w", err)
	}
	return h, nil
}

// initPlugins initializes required plugins for the processor.
func (h *lookUpHandler) initPlugins(ctx context.Context, mgr PluginManager, cfg *PluginCfg) error {
	var err error
	if cfg.Cache == nil {
		return fmt.Errorf("invalid config: Cache missing")
	}
	if h.cache, err = mgr.Cache(ctx, cfg.Cache); err != nil {
		return fmt.Errorf("failed to load cache: %w", err)
	}
	return nil
}

// LookupHandler handles the lookup requests.
func (h *lookUpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Debug(r.Context(), "Reg Lookup handler called.")
	var req model.Subscription
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Use the SubscriberID as the cache key
	cacheKey := fmt.Sprintf("subscriber:%s", req.SubscriberID)

	cachedValue, err := h.cache.Get(ctx, cacheKey)
	if err != nil {
		http.Error(w, "Subscriber ID not found", http.StatusNotFound)
		return
	}

	var subData model.Subscription
	err = json.Unmarshal([]byte(cachedValue), &subData)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Errorf(r.Context(), err, "Error unmarshaling cached data")
		return
	}

	// Send the result as the response
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode([]model.Subscription{subData})
	if err != nil {
		log.Errorf(r.Context(), err, "Error encoding JSON")
	}
}
