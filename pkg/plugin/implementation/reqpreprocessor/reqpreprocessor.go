package reqpreprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/beckn/beckn-onix/pkg/log"
	"github.com/beckn/beckn-onix/pkg/model"
	"github.com/google/uuid"
)

// Config holds the configuration settings for the application.
type Config struct {
	ContextKeys []string // ContextKeys is a list of context keys used for request processing.
	Role        string   // Role specifies the role of the entity (e.g., subscriber, gateway).
}

type becknRequest struct {
	Context map[string]any `json:"context"`
}

const contextKey = "context"

// NewPreProcessor creates a middleware that processes incoming HTTP requests by extracting
// and modifying the request context based on the provided configuration.
func NewPreProcessor(cfg *Config) (func(http.Handler) http.Handler, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var req becknRequest
			ctx := r.Context()
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "Failed to decode request body", http.StatusBadRequest)
				return
			}
			if req.Context == nil {
				http.Error(w, fmt.Sprintf("%s field not found.", contextKey), http.StatusBadRequest)
				return
			}
			var subID any
			switch cfg.Role {
			case "bap":
				subID = req.Context["bap_id"]
			case "bpp":
				subID = req.Context["bpp_id"]
			}
			if subID != nil {
				log.Debugf(ctx, "adding subscriberId to request:%s, %v", model.SubscriberIDKey, subID)
				// TODO: Add a ContextKey type in model and use it here instead of raw context key.
				ctx = context.WithValue(ctx, model.SubscriberIDKey, subID)
			}
			for _, key := range cfg.ContextKeys {
				value := uuid.NewString()
				updatedValue := update(req.Context, key, value)
				ctx = context.WithValue(ctx, model.ContextKey(key), updatedValue)
			}
			reqData := map[string]any{"context": req.Context}
			updatedBody, _ := json.Marshal(reqData)
			r.Body = io.NopCloser(bytes.NewBuffer(updatedBody))
			r.ContentLength = int64(len(updatedBody))
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}, nil
}

func update(wrapper map[string]any, key string, value any) any {
	field, exists := wrapper[key]
	if !exists || isEmpty(field) {
		wrapper[key] = value
		return value
	}

	return field
}
func isEmpty(v any) bool {
	switch v := v.(type) {
	case string:
		return v == ""
	case nil:
		return true
	default:
		return false
	}
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config cannot be nil")
	}

	// Check if ContextKeys is empty.
	if len(cfg.ContextKeys) == 0 {
		return errors.New("ContextKeys cannot be empty")
	}

	// Validate that ContextKeys does not contain empty strings.
	for _, key := range cfg.ContextKeys {
		if key == "" {
			return errors.New("ContextKeys cannot contain empty strings")
		}
	}
	return nil
}
