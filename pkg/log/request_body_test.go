package log

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// lastRequestEntry returns the most recent "HTTP Request" entry in the log
// file, which earlier tests in this package also write to.
func lastRequestEntry(t *testing.T, logPath string) map[string]any {
	t.Helper()
	var entry map[string]any
	for _, line := range readLogFile(t, logPath) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if e := parseLogLine(t, line); e["message"] == "HTTP Request" {
			entry = e
		}
	}
	if entry == nil {
		t.Fatal("no HTTP Request entry was logged")
	}
	return entry
}

func TestRequestTruncatesLargeBody(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	body := []byte(strings.Repeat("a", maxLoggedBodyBytes+1000))
	req, _ := http.NewRequest("POST", "/api/test", nil)

	Request(context.Background(), req, body)

	entry := lastRequestEntry(t, logPath)
	if logged, _ := entry["body"].(string); len(logged) != maxLoggedBodyBytes {
		t.Errorf("logged body is %d bytes, want it cut to %d", len(logged), maxLoggedBodyBytes)
	}
	if size, _ := entry["bodyBytes"].(float64); int(size) != len(body) {
		t.Errorf("bodyBytes = %v, want the full size %d", entry["bodyBytes"], len(body))
	}
	if entry["bodyTruncated"] != true {
		t.Errorf("bodyTruncated = %v, want true", entry["bodyTruncated"])
	}
}

func TestRequestLogsSmallBodyWhole(t *testing.T) {
	logPath := setupLogger(t, InfoLevel)
	req, _ := http.NewRequest("POST", "/api/test", nil)

	Request(context.Background(), req, []byte(`{"key":"value"}`))

	entry := lastRequestEntry(t, logPath)
	if entry["body"] != `{"key":"value"}` {
		t.Errorf("body is %d bytes, want the 15 byte body unchanged", len(entry["body"].(string)))
	}
	if _, ok := entry["bodyTruncated"]; ok {
		t.Error("bodyTruncated set on a body under the cap")
	}
}
