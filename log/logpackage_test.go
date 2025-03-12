package logpackage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

func setupTest(t *testing.T) string {
	once = sync.Once{}

	tempDir, err := os.MkdirTemp("", "logger-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	return tempDir
}

func TestInitLoggerWithValidConfig(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "test.log")

	InitLogger(LoggerConfig{
		Level:       "debug",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id", "user_id"},
	})

	if logger.GetLevel() == zerolog.Disabled {
		t.Error("Logger was not initialized")
	}

	ctx := context.WithValue(context.Background(), "request_id", "test-123")
	Debug(ctx, "debug message")
	Info(ctx, "info message")
	Warn(ctx, "warning message")
	Error(ctx, errors.New("test error"), "error message")

	if _, err := os.Stat(testLogPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", testLogPath)
	}
}

func TestInitLoggerWithInvalidLevel(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "test.log")

	InitLogger(LoggerConfig{
		Level:       "invalid_level",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id"},
	})

	if logger.GetLevel() == zerolog.Disabled {
		t.Error("Logger was not initialized")
	}

	ctx := context.WithValue(context.Background(), "request_id", "test-123")
	Info(ctx, "info message")

	if _, err := os.Stat(testLogPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", testLogPath)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "log.yaml")
	configContent := `level: debug
file_path: /tmp/test.log
max_size: 10
max_backups: 3
max_age: 7
context_keys:
  - request_id
  - user_id`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	originalGetConfigPath := getConfigPath
	defer func() { getConfigPath = originalGetConfigPath }()
	getConfigPath = func() (string, error) {
		return configPath, nil
	}

	config, err := loadConfig()
	if err != nil {
		t.Errorf("loadConfig() error = %v", err)
	}

	if config.Level != "debug" {
		t.Errorf("Expected level 'debug', got '%s'", config.Level)
	}
	if config.FilePath != "/tmp/test.log" {
		t.Errorf("Expected file_path '/tmp/test.log', got '%s'", config.FilePath)
	}
	if len(config.ContextKeys) != 2 {
		t.Errorf("Expected 2 context keys, got %d", len(config.ContextKeys))
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "non-existent.yaml")

	originalGetConfigPath := getConfigPath
	defer func() { getConfigPath = originalGetConfigPath }()
	getConfigPath = func() (string, error) {
		return configPath, nil
	}

	_, err := loadConfig()
	if err == nil {
		t.Error("Expected error for non-existent config file, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "invalid.yaml")
	configContent := `level: debug
file_path: /tmp/test.log
max_size: invalid
max_backups: 3`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	originalGetConfigPath := getConfigPath
	defer func() { getConfigPath = originalGetConfigPath }()
	getConfigPath = func() (string, error) {
		return configPath, nil
	}

	_, err = loadConfig()
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

func TestFatal(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "fatal.log")

	originalExitFunc := ExitFunc
	defer func() { ExitFunc = originalExitFunc }()

	var exitCalled bool
	var exitCode int
	ExitFunc = func(code int) {
		exitCalled = true
		exitCode = code

	}

	InitLogger(LoggerConfig{
		Level:       "debug",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id"},
	})

	ctx := context.WithValue(context.Background(), "request_id", "test-fatal")
	Fatal(ctx, "fatal message")

	if !exitCalled {
		t.Error("ExitFunc was not called")
	}
	if exitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", exitCode)
	}

	if _, err := os.Stat(testLogPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", testLogPath)
	}

	content, err := os.ReadFile(testLogPath)
	if err != nil {
		t.Errorf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "fatal message") {
		t.Error("Log file does not contain fatal message")
	}
}

func TestLoggingWithContext(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "context.log")

	// Initialize logger with context keys
	InitLogger(LoggerConfig{
		Level:       "debug",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id", "user_id", "session_id"},
	})

	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "req-123")
	ctx = context.WithValue(ctx, "user_id", "user-456")
	Info(ctx, "message with context")

	if _, err := os.Stat(testLogPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", testLogPath)
	}

	content, err := os.ReadFile(testLogPath)
	if err != nil {
		t.Errorf("Failed to read log file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "req-123") {
		t.Error("Log file does not contain request_id")
	}
	if !strings.Contains(contentStr, "user-456") {
		t.Error("Log file does not contain user_id")
	}
}

func TestFormattedLogging(t *testing.T) {
	once = sync.Once{}

	tempDir, err := os.MkdirTemp("", "logger-format-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "formatted.log")

	InitLogger(LoggerConfig{
		Level:       "debug",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id"},
	})

	ctx := context.WithValue(context.Background(), "request_id", "format-test-123")

	testValues := []struct {
		name     string
		number   int
		text     string
		expected string
	}{
		{
			name:     "debug",
			number:   42,
			text:     "formatted debug",
			expected: "formatted debug message #42",
		},
		{
			name:     "info",
			number:   100,
			text:     "formatted info",
			expected: "formatted info message #100",
		},
		{
			name:     "warn",
			number:   200,
			text:     "formatted warning",
			expected: "formatted warning message #200",
		},
		{
			name:     "error",
			number:   500,
			text:     "formatted error",
			expected: "formatted error message #500",
		},
	}

	for _, tv := range testValues {
		t.Run(tv.name+"f", func(t *testing.T) {
			format := "%s message #%d"

			switch tv.name {
			case "debug":
				Debugf(ctx, format, tv.text, tv.number)
			case "info":
				Infof(ctx, format, tv.text, tv.number)
			case "warn":
				Warnf(ctx, format, tv.text, tv.number)
			case "error":
				testErr := errors.New("test error")
				Errorf(ctx, testErr, format, tv.text, tv.number)
			}
		})
	}
}

func TestLoggingWithNonStringContext(t *testing.T) {
	tempDir := setupTest(t)
	defer os.RemoveAll(tempDir)

	testLogPath := filepath.Join(tempDir, "non-string.log")

	InitLogger(LoggerConfig{
		Level:       "debug",
		FilePath:    testLogPath,
		MaxSize:     10,
		MaxBackups:  3,
		MaxAge:      7,
		ContextKeys: []string{"request_id", "count"},
	})

	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "req-123")
	ctx = context.WithValue(ctx, "count", 42)

	Info(ctx, "message with non-string context")

	if _, err := os.Stat(testLogPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", testLogPath)
	}

	content, err := os.ReadFile(testLogPath)
	if err != nil {
		t.Errorf("Failed to read log file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "req-123") {
		t.Error("Log file does not contain request_id")
	}

}

func TestGetConfigPath(t *testing.T) {
	path, err := getConfigPath()
	if err != nil {
		t.Errorf("getConfigPath() error = %v", err)
	}
	if path == "" {
		t.Error("getConfigPath() returned empty path")
	}
}

func TestGetConfigPathError(t *testing.T) {

	originalGetConfigPath := getConfigPath
	defer func() { getConfigPath = originalGetConfigPath }()

	expectedErr := errors.New("runtime caller error")
	getConfigPath = func() (string, error) {
		return "", expectedErr
	}

	once = sync.Once{}
	InitLogger()
	ctx := context.Background()
	Info(ctx, "info after config failure")
}
