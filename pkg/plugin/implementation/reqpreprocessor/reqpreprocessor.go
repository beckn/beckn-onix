package reqpreprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// writeCodedError writes a Beckn v2.0.0 ErrorCode-aligned JSON error body.
// reqpreprocessor runs as HTTP middleware ahead of the standard step/NACK
// pipeline (see core/module/handler/stdHandler.go's step loop), so this is a
// correctly-coded but unsigned JSON body, not the full Beckn NACK envelope
// nack() produces elsewhere — wiring this plugin into that pipeline (with the
// signing/telemetry implications that carries) is tracked separately in #868.
func writeCodedError(ctx context.Context, w http.ResponseWriter, status int, code, message string) {
	// model.NewCodedError's result is a two-string-field struct that cannot
	// realistically fail to marshal — matching nack()'s own established
	// convention (core/module/handler/responsestep.go) of not guarding this.
	data, _ := json.Marshal(model.NewCodedError(code, message))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, werr := w.Write(data); werr != nil {
		log.Debugf(ctx, "failed to write coded error response: %v", werr)
		http.Error(w, message, http.StatusInternalServerError)
	}
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
			ctx := r.Context()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeCodedError(ctx, w, http.StatusBadRequest, "SCH_INVALID_JSON", "Failed to read request body")
				return
			}

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

			// Decode the body and extract its context field using the same
			// classification reqmapper (#867) relies on for the identical check.
			_, reqContext, becknErr, _ := model.ExtractContext(body)
			if becknErr != nil {
				writeCodedError(ctx, w, http.StatusBadRequest, becknErr.Code, becknErr.Message)
				return
			}

			// Resolve subscriber ID and caller ID using shared utilities that handle
			// the triple-key alias chain (legacy snake_case, camelCase, spec v2 names).
			subID := model.ResolveSubscriberID(reqContext, model.Role(cfg.Role))
			callerID := model.ResolveCallerID(reqContext, model.Role(cfg.Role))

			if subID != "" {
				log.Debugf(ctx, "adding subscriberId to request:%s, %v", model.ContextKeySubscriberID, subID)
				ctx = context.WithValue(ctx, model.ContextKeySubscriberID, subID)
			}

			if cfg.ParentID != "" {
				log.Debugf(ctx, "adding parentID to request:%s, %v", model.ContextKeyParentID, cfg.ParentID)
				ctx = context.WithValue(ctx, model.ContextKeyParentID, cfg.ParentID)
			}

			if callerID != "" {
				log.Debugf(ctx, "adding callerID to request:%s, %v", model.ContextKeyRemoteID, callerID)
				ctx = context.WithValue(ctx, model.ContextKeyRemoteID, callerID)
			}

			if networkID := model.ResolveNetworkID(reqContext); networkID != "" {
				log.Debugf(ctx, "adding networkID to request:%s, %v", model.ContextKeyNetworkID, networkID)
				ctx = context.WithValue(ctx, model.ContextKeyNetworkID, networkID)
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
