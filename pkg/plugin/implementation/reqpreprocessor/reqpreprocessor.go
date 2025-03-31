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
)

type Config struct {
	ContextKeys []string
	Role        string
}

type keyType string

const (
	contextKey      keyType = "context"
	subscriberIDKey keyType = "subscriber_id"
)

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

			// Extract context from request
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
				log.Debugf(ctx, "adding subscriberId to request:%s, %v", subscriberIDKey, subID)
				ctx = context.WithValue(ctx, subscriberIDKey, subID)
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
	return nil
}
