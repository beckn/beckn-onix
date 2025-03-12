package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// MockPublisher implements the plugin.Publisher interface
type mockPublisher struct {
	mu        sync.Mutex
	messages  []string
	shouldErr bool
}

func newMockPublisher(shouldErr bool) *mockPublisher {
	return &mockPublisher{
		messages:  make([]string, 0),
		shouldErr: shouldErr,
	}
}

func (m *mockPublisher) Publish(message string) error {
	if m.shouldErr {
		return fmt.Errorf("mock publish error")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, message)
	return nil
}

func (m *mockPublisher) Handle(message string) error {
	return m.Publish(message)
}

func (m *mockPublisher) Configure(config map[string]interface{}) error {
	if m.shouldErr {
		return fmt.Errorf("mock configure error")
	}
	return nil
}

// Helper function to create temporary config file
func createTempConfig(t *testing.T, content string) string {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}
	return tmpfile.Name()
}

// Helper function to create test plugin directory and files
func setupTestPlugins(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "plugins-*")
	if err != nil {
		t.Fatalf("Failed to create temp plugin directory: %v", err)
	}

	// Create a dummy plugin file
	pluginPath := filepath.Join(tmpDir, "test-publisher.so")
	if err := os.WriteFile(pluginPath, []byte("dummy plugin"), 0644); err != nil {
		t.Fatalf("Failed to create dummy plugin file: %v", err)
	}

	return tmpDir
}

// Success Test Cases
func TestInitConfigSuccess(t *testing.T) {
	pluginDir := setupTestPlugins(t)
	defer os.RemoveAll(pluginDir)

	tests := []struct {
		name       string
		configData string
		want       *config
	}{
		{
			name: "Valid minimal config",
			configData: fmt.Sprintf(`
appName: "testApp"
port: 8080
plugin:
  root: "%s"
  publisher:
    id: "test-publisher"
    config:
      project: "test"
      topic: "test-topic"
`, pluginDir),
			want: &config{
				AppName: "testApp",
				Port:    8080,
				Plugin: PluginConfig{
					Root: pluginDir,
					Publisher: PublisherConfig{
						ID: "test-publisher",
						Config: map[string]interface{}{
							"project": "test",
							"topic":   "test-topic",
						},
					},
				},
			},
		},
		{
			name: "Valid config with optional fields",
			configData: fmt.Sprintf(`
appName: "testApp2"
port: 9090
plugin:
  root: "%s"
  publisher:
    id: "test-publisher"
    config:
      project: "test2"
      topic: "test-topic2"
      region: "us-west"
`, pluginDir),
			want: &config{
				AppName: "testApp2",
				Port:    9090,
				Plugin: PluginConfig{
					Root: pluginDir,
					Publisher: PublisherConfig{
						ID: "test-publisher",
						Config: map[string]interface{}{
							"project": "test2",
							"topic":   "test-topic2",
							"region":  "us-west",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configData)
			defer os.Remove(configPath)

			got, err := initConfig(context.Background(), configPath)
			if err != nil {
				t.Fatalf("initConfig() error = %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("initConfig() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerHandlerSuccess(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		body         string
		expectedCode int
	}{
		{
			name:         "Valid POST request",
			method:       http.MethodPost,
			body:         `{"test": "data"}`,
			expectedCode: http.StatusOK,
		},
		{
			name:         "Empty body POST",
			method:       http.MethodPost,
			body:         "",
			expectedCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := newMockPublisher(false)
			s := &server{publisher: pub}

			req := httptest.NewRequest(tt.method, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			s.handler(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("handler() status = %v, want %v", w.Code, tt.expectedCode)
			}
		})
	}
}

func TestRunSuccess(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (string, func())
	}{
		{
			name: "Success with valid config and plugin",
			setupFunc: func(t *testing.T) (string, func()) {
				// Use the existing plugin directory
				pluginDir := "../../plugins/publisher"

				// Verify plugin file exists
				if _, err := os.Stat(filepath.Join(pluginDir, "publisher.so")); err != nil {
					t.Skipf("Plugin file not found. Please ensure publisher.so exists in %s", pluginDir)
				}

				configData := fmt.Sprintf(`
appName: "testApp"
port: 8080
plugin:
  root: "%s"
  publisher:
    id: "publisher"
    config:
      project: "test"
      topic: "test-topic"
`, pluginDir)
				configPath := createTempConfig(t, configData)
				cleanup := func() {
					os.Remove(configPath)
				}
				return configPath, cleanup
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, cleanup := tt.setupFunc(t)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := run(ctx, configPath)
			if err != nil && !strings.Contains(err.Error(), "context deadline exceeded") {
				t.Errorf("run() unexpected error = %v", err)
			}
		})
	}
}

// Failure Test Cases
func TestInitConfigFailure(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		wantErr    bool
	}{
		{
			name: "Invalid YAML",
			configData: `
appName: "testApp"
port: invalid
`,
			wantErr: true,
		},
		{
			name: "Missing required fields",
			configData: `
port: 8080
`,
			wantErr: true,
		},
		{
			name: "Invalid port range",
			configData: `
appName: "testApp"
port: 80
plugin:
  root: "/plugins"
  publisher:
    id: "test-publisher"
`,
			wantErr: true,
		},
		{
			name: "Missing plugin root",
			configData: `
appName: "testApp"
port: 8080
plugin:
  publisher:
    id: "test-publisher"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configData)
			defer os.Remove(configPath)

			_, err := initConfig(context.Background(), configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("initConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestServerHandlerFailure(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		body         string
		publisherErr bool
		expectedCode int
	}{
		{
			name:         "Invalid method GET",
			method:       http.MethodGet,
			body:         "",
			publisherErr: false,
			expectedCode: http.StatusMethodNotAllowed,
		},
		{
			name:         "Publisher error",
			method:       http.MethodPost,
			body:         `{"test": "data"}`,
			publisherErr: true,
			expectedCode: http.StatusOK, // Still OK because publish is async
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := newMockPublisher(tt.publisherErr)
			s := &server{publisher: pub}

			req := httptest.NewRequest(tt.method, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			s.handler(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("handler() status = %v, want %v", w.Code, tt.expectedCode)
			}
		})
	}
}

func TestRunFailure(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (string, string, func())
		errSubstr string
	}{
		{
			name: "Failure with non-existent config",
			setupFunc: func(t *testing.T) (string, string, func()) {
				return "nonexistent.yaml", "", func() {}
			},
			errSubstr: "no such file",
		},
		{
			name: "Failure with invalid plugin path",
			setupFunc: func(t *testing.T) (string, string, func()) {
				configData := `
appName: "testApp"
port: 8080
plugin:
  root: "/nonexistent/plugins"
  publisher:
    id: "test-publisher"
    config:
      project: "test"
      topic: "test-topic"
`
				configPath := createTempConfig(t, configData)
				return configPath, "", func() { os.Remove(configPath) }
			},
			errSubstr: "failed to load publisher plugin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath, _, cleanup := tt.setupFunc(t)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := run(ctx, configPath)
			if err == nil || !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("run() error = %v, want error containing %q", err, tt.errSubstr)
			}
		})
	}
}

// Main Function Test
func TestMain(t *testing.T) {
	pluginDir := setupTestPlugins(t)
	defer os.RemoveAll(pluginDir)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	configData := fmt.Sprintf(`
appName: "testApp"
port: 8080
plugin:
  root: "%s"
  publisher:
    id: "test-publisher"
    config:
      project: "test"
      topic: "test-topic"
`, pluginDir)

	configPath := createTempConfig(t, configData)
	defer os.Remove(configPath)

	os.Args = []string{"cmd", "-config", configPath}

	// Use a channel to catch potential panics
	done := make(chan bool)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("main() panic = %v", r)
			}
			done <- true
		}()
		main()
	}()

	// Wait for either completion or timeout
	select {
	case <-done:
		// Test passed
	case <-time.After(2 * time.Second):
		t.Error("main() test timed out")
	}
}

func TestRequestHandler(t *testing.T) {
	srv := &server{
		publisher: &mockPublisher{},
	}

	tests := []struct {
		name       string
		method     string
		expectCode int
	}{
		{
			name:       "Success - POST request",
			method:     http.MethodPost,
			expectCode: http.StatusOK,
		},
		{
			name:       "Fail - GET request",
			method:     http.MethodGet,
			expectCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/", nil)
			rr := httptest.NewRecorder()

			srv.handler(rr, req)

			if rr.Code != tt.expectCode {
				t.Errorf("Expected status code %d, got %d", tt.expectCode, rr.Code)
			}
		})
	}
}
