package log

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"time"

	// "bytes"
	// "fmt"
	// "net/http"
	// "time"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testLogFilePath = "./test_logs/test.log"

type ctxKey any

var requestID ctxKey = "requestID"
var userID ctxKey = "userID"

func setupLogger(t *testing.T, l Level) string {
	dir := filepath.Dir(testLogFilePath)
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		t.Fatalf("failed to create test log directory: %v", err)
	}

	config := Config{
		level: l,
		destinations: []Destination{
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
		contextKeys: []any{"userID", "requestID"},
	}

	err = InitLogger(config)
	if err != nil {
		t.Fatalf("failed to initialize logger: %v", err)
	}

	return testLogFilePath
}

func readLogFile(t *testing.T, logPath string) []string {
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("failed to open log file: %v", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	return lines
}

func parseLogLine(t *testing.T, line string) map[string]interface{} {
	var logEntry map[string]interface{}
	err := json.Unmarshal([]byte(line), &logEntry)
	if err != nil {
		t.Fatalf("failed to parse log entry: %v", err)
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
		if logEntry["level"] == "debug" && strings.Contains(logEntry["message"].(string), "Debugf message") {
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
		if logEntry["level"] == "debug" && strings.Contains(logEntry["message"].(string), "Debugf message") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected formatted debug message, but it was not found in logs")
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
