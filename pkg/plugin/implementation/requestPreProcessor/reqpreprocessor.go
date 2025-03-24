package requestpreprocessor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

type Config struct {
	CheckKeys []string
	Role      string
}

type BecknRequest struct {
	Context map[string]any `json:"context"`
}

type contextKeyType string

const contextKey = "context"
const subscriberIDKey contextKeyType = "subscriber_id"

func NewUUIDSetter(cfg *Config) (func(http.Handler) http.Handler, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			var req BecknRequest
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
			ctx := context.WithValue(r.Context(), subscriberIDKey, subID)
			for _, key := range cfg.CheckKeys {
				value := uuid.NewString()
				updatedValue := update(req.Context, key, value)
				ctx = context.WithValue(ctx, contextKeyType(key), updatedValue)
			}
			reqData := map[string]any{"context": req.Context}
			updatedBody, err := json.Marshal(reqData)
			if err != nil {
				http.Error(w, "Failed to marshal updated JSON", http.StatusInternalServerError)
				return
			}
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
	for _, key := range cfg.CheckKeys {
		if key == "" {
			return errors.New("checkKeys cannot contain empty strings")
		}
	}
	return nil
}
