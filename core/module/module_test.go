package module

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/beckn/beckn-onix/core/module/handler"
	"github.com/beckn/beckn-onix/plugin"
)

// MockPluginManager implements the PluginManager interface for testing purposes.
type MockPluginManager struct {
	middlewareFunc func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error)
}

// Middleware returns a middleware function for testing based on the mock setup.
func (m *MockPluginManager) Middleware(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
	if m.middlewareFunc != nil {
		return m.middlewareFunc(ctx, cfg)
	}
	return nil, nil
}

// mockHandlerProvider is a mock implementation of a handler provider function for testing.
func mockHandlerProvider(ctx context.Context, mgr *plugin.Manager, cfg *handler.Config) (http.Handler, error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Mocked Response"))
	}), nil
}

// TestRegister tests the Register function with various scenarios.
func TestRegister(t *testing.T) {
	tests := []struct {
		name          string
		moduleConfigs []Config
		mockSetup     func(mgr *MockPluginManager)
		expectedError string
	}{
		{
			name: "Valid configuration",
			moduleConfigs: []Config{
				{
					Name: "module1",
					Path: "/module1",
					Handler: handler.Config{
						Type: handler.HandlerTypeStd,
					},
				},
			},
			mockSetup: func(mgr *MockPluginManager) {
				mgr.middlewareFunc = func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
					return func(h http.Handler) http.Handler {
						return h // Return the handler unchanged
					}, nil
				}
			},
			expectedError: "",
		},
		{
			name: "Invalid handler type",
			moduleConfigs: []Config{
				{
					Name: "module2",
					Path: "/module2",
					Handler: handler.Config{
						Type: "invalid_type", // Invalid handler type
					},
				},
			},
			mockSetup:     func(mgr *MockPluginManager) {},
			expectedError: "invalid module : module2",
		},
		{
			name: "Handler provider error",
			moduleConfigs: []Config{
				{
					Name: "module3",
					Path: "/module3",
					Handler: handler.Config{
						Type: handler.HandlerTypeStd,
					},
				},
			},
			mockSetup: func(mgr *MockPluginManager) {
				mgr.middlewareFunc = func(ctx context.Context, cfg *plugin.Config) (func(http.Handler) http.Handler, error) {
					return nil, errors.New("middleware error")
				}
			},
			expectedError: "failed to add post processors: middleware error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new context
			ctx := context.Background()

			// Create a new ServeMux
			mux := http.NewServeMux()

			// Create a mock plugin manager
			mgr := &MockPluginManager{}
			if tt.mockSetup != nil {
				tt.mockSetup(mgr)
			}

			// Register the handler provider
			handlerProviders[handler.HandlerTypeStd] = mockHandlerProvider

			// Call the Register function
			err := Register(ctx, tt.moduleConfigs, mux, nil)

			// Check the expected error
			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if err.Error() != tt.expectedError {
					t.Errorf("expected error %q but got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got %q", err.Error())
				}
			}
		})
	}
}
