package reqpreprocessor

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
	checkKeys []string
}

const contextKey = "context"

func NewUUIDSetter(cfg *Config) (func(http.Handler) http.Handler, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			var data map[string]any
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			if err := json.Unmarshal(body, &data); err != nil {
				http.Error(w, "Failed to decode request body", http.StatusBadRequest)
				return
			}
			contextRaw := data[contextKey]
			if contextRaw == nil {
				http.Error(w, fmt.Sprintf("%s field not found.", contextKey), http.StatusBadRequest)
				return
			}
			contextData, ok := contextRaw.(map[string]any)
			if !ok {
				http.Error(w, fmt.Sprintf("%s field is not a map.", contextKey), http.StatusBadRequest)
				return
			}
			ctx := r.Context()
			for _, key := range cfg.checkKeys {
				value := uuid.NewString()
				updatedValue := update(contextData, key, value)
				ctx = context.WithValue(ctx, key, updatedValue)
			}
			data[contextKey] = contextData
			updatedBody, err := json.Marshal(data)
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
	if len(cfg.checkKeys) == 0 {
		return errors.New("checkKeys cannot be empty")
	}
	for _, key := range cfg.checkKeys {
		if key == "" {
			return errors.New("checkKeys cannot contain empty strings")
		}
	}
	return nil
}
