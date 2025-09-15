package reqpreprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Config represents the configuration for the request preprocessor middleware.
type Config struct {
	Role        string
	ContextKeys []string
}

const contextKey = "context"

// NewPreProcessor returns a middleware that processes the incoming request,
// extracts the context field from the body, and adds relevant values (like subscriber ID).
func NewPreProcessor(cfg *Config) (func(http.Handler) http.Handler, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			var req map[string]interface{}
			ctx := r.Context()
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "Failed to decode request body", http.StatusBadRequest)
				return
			}

			// Extract context from request.
			reqContext, ok := req["context"].(map[string]interface{})
			if !ok {
				http.Error(w, fmt.Sprintf("%s field not found or invalid.", contextKey), http.StatusBadRequest)
				return
			}
			var subID any
			switch cfg.Role {
			case "bap":
				subID = reqContext["bap_id"]
			case "bpp":
				subID = reqContext["bpp_id"]
			}
			if subID != nil {
				log.Debugf(ctx, "adding subscriberId to request:%s, %v", model.ContextKeySubscriberID, subID)
				ctx = context.WithValue(ctx, model.ContextKeySubscriberID, subID)
			}
			for _, key := range cfg.ContextKeys {
				ctxKey, _ := model.ParseContextKey(key)
				if v, ok := reqContext[key]; ok {
					ctx = context.WithValue(ctx, ctxKey, v)
				}
			}
			r.Body = io.NopCloser(bytes.NewBuffer(body))
			r.ContentLength = int64(len(body))
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}, nil
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config cannot be nil")
	}

	if cfg.Role != "bap" && cfg.Role != "bpp" {
		return errors.New("role must be either 'bap' or 'bpp'")
	}

	for _, key := range cfg.ContextKeys {
		if _, err := model.ParseContextKey(key); err != nil {
			return err
		}
	}
	return nil
}
