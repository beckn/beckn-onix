package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestLogFunctions(t *testing.T) {
	testConfig := Config{
		level: DebugLevel,
		destinations: []Destination{
			{
				Type: File,
				Config: map[string]string{
					"path":       "log/app.txt",
					"maxSize":    "500",
					"maxBackups": "15",
					"maxAge":     "30",
				},
			},
		},
		contextKeys: []any{"userID", "requestID"},
	}
	err := InitLogger(testConfig)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}

	tests := []struct {
		name           string
		logFunc        func(ctx context.Context)
		expectedOutput string
	}{
		{
			name: "Debug log with context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Debug(ctx, "debug message")
			},
			expectedOutput: `{"level":"debug","requestID":"12345","message":"debug message"}`,
		},
		{
			name: "Debugf with context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Debugf(ctx, "formatted %s", "debug message")
			},
			expectedOutput: `{"level":"debug","requestID":"12345","message":"formatted debug message"}`,
		},
		{
			name: "Info log with message",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Info(ctx, "info message")
			},
			expectedOutput: `{"level":"info","requestID":"12345","message":"info message"}`,
		},

		{
			name: "Info log with formatted message",
			logFunc: func(ctx context.Context) {
				Infof(ctx, "formatted %s", "info message")
			},
			expectedOutput: `{"level":"info","message":"formatted info message"}`,
		},
		{
			name: "Warn log with context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Warn(ctx, "warning message")
			},
			expectedOutput: `{"level":"warn","requestID":"12345","message":"warning message"}`,
		},
		{
			name: "Warnf with context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Warnf(ctx, "formatted %s", "warning message")
			},
			expectedOutput: `{"level":"warn","requestID":"12345","message":"formatted warning message"}`,
		},
		{
			name: "Error log with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var userID ctxKey = "userID"

				ctx = context.WithValue(ctx, userID, "67890")
				Error(ctx, errors.New("something went wrong"), "error message")
			},
			expectedOutput: `{"level":"error","userID":"67890","error":"something went wrong","message":"error message"}`,
		},
		{
			name: "Errorf with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var userID ctxKey = "userID"

				ctx = context.WithValue(ctx, userID, "67890")
				Errorf(ctx, errors.New("something went wrong"), "formatted %s", "error message")
			},
			expectedOutput: `{"level":"error","userID":"67890","error":"something went wrong","message":"formatted error message"}`,
		},
		{
			name: "Fatal log with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Fatal(ctx, errors.New("fatal error"), "fatal message")
			},
			expectedOutput: `{"level":"fatal","requestID":"12345","error":"fatal error","message":"fatal message"}`,
		},
		{
			name: "Fatalf with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var requestID ctxKey = "requestID"

				ctx = context.WithValue(ctx, requestID, "12345")
				Fatalf(ctx, errors.New("fatal error"), "formatted %s", "fatal message")
			},
			expectedOutput: `{"level":"fatal","requestID":"12345","error":"fatal error","message":"formatted fatal message"}`,
		},
		{
			name: "Panic log with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var userID ctxKey = "userID"

				ctx = context.WithValue(ctx, userID, "67890")
				Panic(ctx, errors.New("panic error"), "panic message")
			},
			expectedOutput: `{"level":"panic","userID":"67890","error":"panic error","message":"panic message"}`,
		},
		{
			name: "Panicf with error and context",
			logFunc: func(ctx context.Context) {
				type ctxKey any
				var userID ctxKey = "userID"

				ctx = context.WithValue(ctx, userID, "67890")
				Panicf(ctx, errors.New("panic error"), "formatted %s", "panic message")
			},
			expectedOutput: `{"level":"panic","userID":"67890","error":"panic error","message":"formatted panic message"}`,
		},
		{
			name: "Request log",
			logFunc: func(ctx context.Context) {
				req, _ := http.NewRequest("GET", "http://example.com", nil)
				req.RemoteAddr = "127.0.0.1:8080"
				Request(ctx, req, []byte("request body"))
			},
			expectedOutput: `{"level":"info","method":"GET","url":"http://example.com","body":"request body","remoteAddr":"127.0.0.1:8080","message":"HTTP Request"}`,
		},
		{
			name: "Response log",
			logFunc: func(ctx context.Context) {
				req, _ := http.NewRequest("GET", "http://example.com", nil)
				Response(ctx, req, 200, 100*time.Millisecond)
			},
			expectedOutput: `{"level":"info","method":"GET","url":"http://example.com","statusCode":200,"responseTime":100,"message":"HTTP Response"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger = zerolog.New(&buf).With().Timestamp().Logger()
			tt.logFunc(context.Background())
			output := buf.String()
			t.Logf("Log output: %s", output)
			lines := strings.Split(strings.TrimSpace(output), "\n")
			if len(lines) == 0 {
				t.Fatal("No log output found")
			}
			lastLine := lines[len(lines)-1]
			var logOutput map[string]interface{}
			if err := json.Unmarshal([]byte(lastLine), &logOutput); err != nil {
				t.Fatalf("Failed to unmarshal log output: %v", err)
			}
			delete(logOutput, "time")
			delete(logOutput, "caller")
			var expectedOutput map[string]interface{}
			if err := json.Unmarshal([]byte(tt.expectedOutput), &expectedOutput); err != nil {
				t.Fatalf("Failed to unmarshal expected output: %v", err)
			}
			for key, expectedValue := range expectedOutput {
				actualValue, ok := logOutput[key]
				if !ok {
					t.Errorf("Expected key %q not found in log output", key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("Mismatch for key %q: expected %v, got %v", key, expectedValue, actualValue)
				}
			}
		})
	}
}
func TestDestinationValidation(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		expectedError error
	}{
		// Missing `path` for File destination
		{
			name: "Missing file path",
			config: Config{
				level: InfoLevel,
				destinations: []Destination{
					{
						Type: File,
						Config: map[string]string{
							"maxSize":    "500",
							"maxBackups": "15",
							"maxAge":     "30",
						},
					},
				},
			},
			expectedError: ErrMissingFilePath,
		},
		{
			name: "Invalid maxAge",
			config: Config{
				level: InfoLevel,
				destinations: []Destination{
					{
						Type: File,
						Config: map[string]string{
							"path":       "log/app.txt",
							"maxSize":    "500",
							"maxBackups": "15",
							"maxAge":     "invalid",
						},
					},
				},
			},
			expectedError: errors.New("invalid maxAge"),
		},
		{
			name: "Valid file destination",
			config: Config{
				level: InfoLevel,
				destinations: []Destination{
					{
						Type: File,
						Config: map[string]string{
							"path":       "log/app.txt",
							"maxSize":    "500",
							"maxBackups": "15",
							"maxAge":     "30",
						},
					},
				},
			},
			expectedError: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Print(tt.config)
			err := InitLogger(tt.config)
			if (err == nil && tt.expectedError != nil) || (err != nil && tt.expectedError == nil) {
				t.Errorf("Expected error: %v, got: %v", tt.expectedError, err)
			} else if err != nil && tt.expectedError != nil && !strings.Contains(err.Error(), tt.expectedError.Error()) {
				t.Errorf("Expected error to contain: %v, got: %v", tt.expectedError, err)
			}
		})
	}
}
