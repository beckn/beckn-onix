package reqpreprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
)

// Config represents the configuration for the request preprocessor middleware.
type Config struct {
	Role        string
	ContextKeys []string
	ParentID    string
}

const contextKey = "context"

// firstNonNil returns the first non-nil value from the provided list.
// Used to resolve context fields that may appear under different key names
// (e.g. bap_id, bapId, or senderId) depending on the beckn spec version in use.
func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// snakeToCamel converts a snake_case string to camelCase.
// For example: "transaction_id" -> "transactionId".
// Returns the input unchanged if it contains no underscores.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

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
			ctx := r.Context()

			// Bodyless requests (GET/DELETE) carry no JSON body and no context
			// fields — skip body extraction and pass through directly.
			// ParentID is a static config value, not derived from the body,
			// so it is injected here before the early return.
			if len(body) == 0 {
				log.Debugf(ctx, "bodyless request to %s: skipping context extraction", r.URL.Path)
				if cfg.ParentID != "" {
					log.Debugf(ctx, "adding parentID to request:%s, %v", model.ContextKeyParentID, cfg.ParentID)
					ctx = context.WithValue(ctx, model.ContextKeyParentID, cfg.ParentID)
				}
				r = r.WithContext(ctx)
				r.Body = io.NopCloser(bytes.NewBuffer(body))
				next.ServeHTTP(w, r)
				return
			}

			var req map[string]interface{}
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

			// Resolve subscriber ID — tries legacy snake_case, then camelCase, then
			// the new Beckn spec v2 names (senderId / receiverId).
			var subID any
			switch cfg.Role {
			case "bap":
				subID = firstNonNil(reqContext["bap_id"], reqContext["bapId"], reqContext["senderId"])
			case "bpp":
				subID = firstNonNil(reqContext["bpp_id"], reqContext["bppId"], reqContext["receiverId"])
			}

			// Resolve caller ID — same triple-key pattern, opposite role.
			var callerID any
			switch cfg.Role {
			case "bap":
				callerID = firstNonNil(reqContext["bpp_id"], reqContext["bppId"], reqContext["receiverId"])
			case "bpp":
				callerID = firstNonNil(reqContext["bap_id"], reqContext["bapId"], reqContext["senderId"])
			}

			if subID != nil {
				log.Debugf(ctx, "adding subscriberId to request:%s, %v", model.ContextKeySubscriberID, subID)
				ctx = context.WithValue(ctx, model.ContextKeySubscriberID, subID)
			}

			if cfg.ParentID != "" {
				log.Debugf(ctx, "adding parentID to request:%s, %v", model.ContextKeyParentID, cfg.ParentID)
				ctx = context.WithValue(ctx, model.ContextKeyParentID, cfg.ParentID)
			}

			if callerID != nil {
				log.Debugf(ctx, "adding callerID to request:%s, %v", model.ContextKeyRemoteID, callerID)
				ctx = context.WithValue(ctx, model.ContextKeyRemoteID, callerID)
			}

			// Extract generic context keys (e.g. transaction_id, message_id).
			// For each configured snake_case key, also try its camelCase equivalent
			// so that a single config entry covers both beckn spec versions.
			for _, key := range cfg.ContextKeys {
				ctxKey, _ := model.ParseContextKey(key)
				if v, ok := reqContext[key]; ok {
					ctx = context.WithValue(ctx, ctxKey, v)
				} else if camelKey := snakeToCamel(key); camelKey != key {
					if v, ok := reqContext[camelKey]; ok {
						ctx = context.WithValue(ctx, ctxKey, v)
					}
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
