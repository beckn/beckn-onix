package log

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestSlogBridge_WarnErrorAttrIsNotDropped(t *testing.T) {
	logPath := setupLogger(t, WarnLevel)
	l := slog.New(NewSlogHandler())
	l.WarnContext(context.Background(), "rescheduling after transient error", "catalogId", "cat-1", "attempts", 1, "error", errors.New("boom"))

	lines := readLogFile(t, logPath)
	var found bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		logEntry := parseLogLine(t, line)
		msg, _ := logEntry["message"].(string)
		if logEntry["level"] == "warn" &&
			strings.Contains(msg, "rescheduling after transient error") &&
			strings.Contains(msg, "catalogId=cat-1") &&
			strings.Contains(msg, "attempts=1") &&
			strings.Contains(msg, "error=boom") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected the error attr to appear in a warn-level message, lines=%v", lines)
	}
}

func TestSlogBridge_InfoErrorAttrIsNotDropped(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	l := slog.New(NewSlogHandler())
	l.InfoContext(context.Background(), "info with error attr", "error", errors.New("oops"))

	lines := readLogFile(t, logPath)
	var found bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		logEntry := parseLogLine(t, line)
		msg, _ := logEntry["message"].(string)
		if logEntry["level"] == "info" && strings.Contains(msg, "error=oops") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected the error attr to appear in an info-level message, lines=%v", lines)
	}
}

func TestSlogBridge_ErrorLevelStillUsesErrorArgument(t *testing.T) {
	logPath := setupLogger(t, ErrorLevel)
	l := slog.New(NewSlogHandler())
	l.ErrorContext(context.Background(), "sync failed permanently", "error", errors.New("fatal"))

	lines := readLogFile(t, logPath)
	var found bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		logEntry := parseLogLine(t, line)
		expected := map[string]interface{}{
			"level":   "error",
			"message": "sync failed permanently",
			"error":   "fatal",
		}
		delete(logEntry, "time")
		if reflect.DeepEqual(expected, logEntry) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a structured error field at Error level, lines=%v", lines)
	}
}

func TestSlogBridge_NoErrorAttrLeavesMessageUnchanged(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	l := slog.New(NewSlogHandler())
	l.InfoContext(context.Background(), "plain info", "key", "value")

	lines := readLogFile(t, logPath)
	var found bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		logEntry := parseLogLine(t, line)
		msg, _ := logEntry["message"].(string)
		if msg == "plain info key=value" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an unchanged message with no error attr, lines=%v", lines)
	}
}
