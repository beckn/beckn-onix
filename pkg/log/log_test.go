package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testLogFilePath = "./test_logs/test.log"

type ctxKey any

var requestID ctxKey = "requestID"
var userID ctxKey = "userID"

func setupLogger(t *testing.T, l level) string {
	t.Helper()
	dir := filepath.Dir(testLogFilePath)
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		t.Fatalf("failed to create test log directory: %v", err)
	}

	config := Config{
		Level: l,
		Destinations: []destination{
			{
				Type: File,
				Config: map[string]string{
					"path":      testLogFilePath,
					"maxSize":   "1",
					"maxAge":    "1",
					"maxBackup": "1",
					"compress":  "false",
				},
			},
		},
		ContextKeys: []string{"userID", "requestID"},
	}
	err = InitLogger(config)
	if err != nil {
		t.Fatalf("failed to initialize logger: %v", err)
	}
	return testLogFilePath
}

func readLogFile(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	return strings.Split(string(b), "\n")
}

func parseLogLine(t *testing.T, line string) map[string]interface{} {
	t.Helper()
	var logEntry map[string]interface{}
	err := json.Unmarshal([]byte(line), &logEntry)
	if err != nil {
		t.Fatalf("Failed to parse log line: %v", err)
	}
	return logEntry
}

func TestDebug(t *testing.T) {
	logPath := setupLogger(t, DebugLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Debug(ctx, "Debug message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "debug" && strings.Contains(logEntry["message"].(string), "Debug message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Debug message, but it was not found in logs")
	}
}

func TestInfo(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Info(ctx, "Info message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "info" && strings.Contains(logEntry["message"].(string), "Info message") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected Info message, but it was not found in logs")
	}
}

func TestWarn(t *testing.T) {
	logPath := setupLogger(t, WarnLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Warn(ctx, "Warning message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "warn" && strings.Contains(logEntry["message"].(string), "Warning message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Warning message, but it was not found in logs")
	}
}

func TestError(t *testing.T) {
	logPath := setupLogger(t, ErrorLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Error(ctx, fmt.Errorf("test error"), "Error message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "error" && strings.Contains(logEntry["message"].(string), "Error message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Error message, but it was not found in logs")
	}
}

func TestRequest(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	ctx := context.WithValue(context.Background(), requestID, "abc-123")
	req, _ := http.NewRequest("POST", "/api/test", bytes.NewBuffer([]byte(`{"key":"value"}`)))
	req.RemoteAddr = "127.0.0.1:8080"
	Request(ctx, req, []byte(`{"key":"value"}`))
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["message"] == "HTTP Request" || logEntry["method"] == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected formatted debug message, but it was not found in logs")
	}
}

func TestResponse(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	ctx := context.WithValue(context.Background(), requestID, "abc-123")
	req, _ := http.NewRequest("GET", "/api/test", nil)
	Response(ctx, req, 200, time.Millisecond*123)
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["message"] == "HTTP Response" {
			if logEntry["message"] == "HTTP Response" {
				value, ok := logEntry["statusCode"]
				if !ok {
					t.Fatalf("Expected key 'statusCode' not found in log entry")
				}
				statusCode, ok := value.(float64)
				if !ok {
					t.Fatalf("Value for 'statusCode' is not a float64, found: %T", value)
				}
				if statusCode == 200 {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Errorf("expected message, but it was not found in logs")
	}
}

func TestFatal(t *testing.T) {
	logPath := setupLogger(t, FatalLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Fatal(ctx, fmt.Errorf("fatal error"), "Fatal message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "fatal" && strings.Contains(logEntry["message"].(string), "Fatal message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Fatal message, but it was not found in logs")
	}
}

func TestPanic(t *testing.T) {
	logPath := setupLogger(t, PanicLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Panic(ctx, fmt.Errorf("panic error"), "Panic message")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "panic" && strings.Contains(logEntry["message"].(string), "Panic message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Panic message, but it was not found in logs")
	}
}

func TestDebugf(t *testing.T) {
	logPath := setupLogger(t, DebugLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Debugf(ctx, "Debugf message: %s", "test")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "debug" && strings.Contains(logEntry["message"].(string), "Debugf message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected formatted debug message, but it was not found in logs")
	}
}

func TestInfof(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Infof(ctx, "Infof message: %s", "test")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "info" && strings.Contains(logEntry["message"].(string), "Infof message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Infof message, but it was not found in logs")
	}
}

func TestWarnf(t *testing.T) {
	logPath := setupLogger(t, WarnLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	Warnf(ctx, "Warnf message: %s", "test")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "warn" && strings.Contains(logEntry["message"].(string), "Warnf message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Warnf message, but it was not found in logs")
	}
}

func TestErrorf(t *testing.T) {
	logPath := setupLogger(t, ErrorLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	err := fmt.Errorf("error message")
	Errorf(ctx, err, "Errorf message: %s", "test")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "error" && strings.Contains(logEntry["message"].(string), "Errorf message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Errorf message, but it was not found in logs")
	}
}

func TestFatalf(t *testing.T) {
	logPath := setupLogger(t, FatalLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	err := fmt.Errorf("fatal error")
	Fatalf(ctx, err, "Fatalf message: %s", "test")
	lines := readLogFile(t, logPath)
	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "fatal" && strings.Contains(logEntry["message"].(string), "Fatalf message") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Fatalf message, but it was not found in logs")
	}
}

func TestPanicf(t *testing.T) {
	logPath := setupLogger(t, PanicLevel)
	ctx := context.WithValue(context.Background(), userID, "12345")
	err := fmt.Errorf("panic error")
	Panicf(ctx, err, "Panicf message: %s", "test")
	lines := readLogFile(t, logPath)

	if len(lines) == 0 {
		t.Fatal("No logs were written.")
	}
	var found bool
	for _, line := range lines {
		logEntry := parseLogLine(t, line)
		if logEntry["level"] == "panic" && strings.Contains(logEntry["message"].(string), "Panicf message") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected Panicf message, but it was not found in logs")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{
			name: "Valid config with Stdout",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{Type: Stdout},
				},
			},
			wantErr: nil,
		},
		{
			name: "Valid config with File destination and valid path",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{
						Type: File,
						Config: map[string]string{
							"path":       "./logs/app.log",
							"maxSize":    "10",
							"maxBackups": "5",
							"maxAge":     "7",
						},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "Error: Invalid log level",
			config: Config{
				Level: "invalid",
				Destinations: []destination{
					{Type: Stdout},
				},
			},
			wantErr: ErrInvalidLogLevel,
		},
		{
			name: "Error: No destinations provided",
			config: Config{
				Level:        InfoLevel,
				Destinations: []destination{},
			},
			wantErr: ErrLogDestinationNil,
		},
		{
			name: "Error: Invalid destination type",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{Type: "unknown"},
				},
			},
			wantErr: fmt.Errorf("invalid destination type 'unknown'"),
		},
		{
			name: "Error: Missing file path for file destination",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{
						Type: File,
						Config: map[string]string{
							"maxSize": "10",
						},
					},
				},
			},
			wantErr: ErrMissingFilePath,
		},
		{
			name: "Error: Invalid maxSize value in file destination",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{
						Type: File,
						Config: map[string]string{
							"path":    "./logs/app.log",
							"maxSize": "invalid",
						},
					},
				},
			},
			wantErr: errors.New(`invalid maxSize: strconv.Atoi: parsing "invalid": invalid syntax`),
		},
		{
			name: "Error: Invalid maxBackups value in file destination",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{
						Type: File,
						Config: map[string]string{
							"path":       "./logs/app.log",
							"maxBackups": "invalid",
						},
					},
				},
			},
			wantErr: errors.New(`invalid maxBackups: strconv.Atoi: parsing "invalid": invalid syntax`),
		},
		{
			name: "Error: Invalid maxAge value in file destination",
			config: Config{
				Level: InfoLevel,
				Destinations: []destination{
					{
						Type: File,
						Config: map[string]string{
							"path":   "./logs/app.log",
							"maxAge": "invalid",
						},
					},
				},
			},
			wantErr: errors.New(`invalid maxAge: strconv.Atoi: parsing "invalid": invalid syntax`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if (err == nil) != (tt.wantErr == nil) {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.wantErr != nil && err.Error() != tt.wantErr.Error() {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
