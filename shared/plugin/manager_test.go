package plugin

import (
	"fmt"
	"os"
	"testing"
)

// MockPlugin implements Plugin interface for testing
type MockPlugin struct {
	handleCalled    bool
	configureCalled bool
	shouldError     bool
}

func (m *MockPlugin) Handle(message string) error {
	m.handleCalled = true
	if m.shouldError {
		return fmt.Errorf("mock handle error")
	}
	return nil
}

func (m *MockPlugin) Configure(config map[string]interface{}) error {
	m.configureCalled = true
	if m.shouldError {
		return fmt.Errorf("mock configure error")
	}
	return nil
}

// MockPublisher implements Publisher interface for testing
type MockPublisher struct {
	MockPlugin
	publishCalled bool
}

func (m *MockPublisher) Publish(message string) error {
	m.publishCalled = true
	if m.shouldError {
		return fmt.Errorf("mock publish error")
	}
	return nil
}

// Helper function to create test config file
func createTestConfig(t *testing.T, content string) string {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp config file: %v", err)
	}
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close config file: %v", err)
	}
	return tmpfile.Name()
}

// Success Test Cases
func TestNewPluginManagerSuccess(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		wantErr    bool
	}{
		{
			name: "Valid minimal config",
			configData: `
plugins:
  test-plugin:
    config:
      key: value
`,
			wantErr: false,
		},
		{
			name: "Valid empty config",
			configData: `
plugins: {}
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTestConfig(t, tt.configData)
			defer os.Remove(configPath)

			pm := NewPluginManager("test-path", configPath)
			if pm == nil {
				t.Error("NewPluginManager() returned nil")
			}
		})
	}
}

func TestRegisterAndGetSuccess(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		plugin     Plugin
	}{
		{
			name:       "Register basic plugin",
			pluginName: "test-plugin",
			plugin:     &MockPlugin{},
		},
		{
			name:       "Register publisher plugin",
			pluginName: "test-publisher",
			plugin:     &MockPublisher{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")
			pm.Register(tt.pluginName, tt.plugin)

			got, err := pm.Get(tt.pluginName)
			if err != nil {
				t.Errorf("Get() error = %v", err)
			}
			if got == nil {
				t.Error("Get() returned nil plugin")
			}
		})
	}
}

func TestGetPublisherSuccess(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		plugin     Publisher
	}{
		{
			name:       "Get valid publisher",
			pluginName: "test-publisher",
			plugin:     &MockPublisher{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")
			pm.Register(tt.pluginName, tt.plugin)

			got, err := pm.GetPublisher(tt.pluginName)
			if err != nil {
				t.Errorf("GetPublisher() error = %v", err)
			}
			if got == nil {
				t.Error("GetPublisher() returned nil")
			}
		})
	}
}

// Failure Test Cases
func TestNewPluginManagerFailure(t *testing.T) {
	tests := []struct {
		name       string
		configData string
		configPath string
		wantErr    bool
	}{
		{
			name:       "Invalid config path",
			configPath: "nonexistent.yaml",
			wantErr:    true,
		},
		{
			name: "Invalid config format",
			configData: `
invalid:
  - yaml: [format
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configPath string
			if tt.configData != "" {
				configPath = createTestConfig(t, tt.configData)
				defer os.Remove(configPath)
			} else {
				configPath = tt.configPath
			}

			pm := NewPluginManager("test-path", configPath)
			if pm.config == nil && !tt.wantErr {
				t.Error("Expected valid config")
			}
		})
	}
}

func TestGetFailure(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		wantErr    bool
	}{
		{
			name:       "Non-existent plugin",
			pluginName: "nonexistent",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")

			_, err := pm.Get(tt.pluginName)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetPublisherFailure(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		plugin     Plugin
		wantErr    bool
	}{
		{
			name:       "Non-publisher plugin",
			pluginName: "test-plugin",
			plugin:     &MockPlugin{},
			wantErr:    true,
		},
		{
			name:       "Non-existent plugin",
			pluginName: "nonexistent",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")
			if tt.plugin != nil {
				pm.Register(tt.pluginName, tt.plugin)
			}

			_, err := pm.GetPublisher(tt.pluginName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPublisher() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPublishMessageFailure(t *testing.T) {
	tests := []struct {
		name      string
		topic     string
		message   string
		setupFunc func(*PluginManager)
		wantErr   bool
	}{
		{
			name:    "Non-existent topic",
			topic:   "nonexistent",
			message: "test message",
			wantErr: true,
		},
		{
			name:    "Plugin error",
			topic:   "test-topic",
			message: "test message",
			setupFunc: func(pm *PluginManager) {
				pm.Register("test-topic", &MockPlugin{shouldError: true})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")
			if tt.setupFunc != nil {
				tt.setupFunc(pm)
			}

			err := pm.PublishMessage(tt.topic, tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("PublishMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadPluginFailure(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		wantErr    bool
	}{
		{
			name:       "Non-existent plugin",
			pluginName: "nonexistent",
			wantErr:    true,
		},
		{
			name:       "Invalid plugin path",
			pluginName: "../invalid/path/plugin",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPluginManager("test-path", "")
			err := pm.LoadPlugin(tt.pluginName)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadPlugin() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
