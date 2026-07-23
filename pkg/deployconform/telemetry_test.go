package deployconform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// deviationReport returns a Report with one modified finding whose detail
// carries a local value ("secret") and one policy finding.
func deviationReport() *Report {
	return &Report{
		NetworkID:      "net-1",
		DevkitID:       "devkit-1",
		ReleaseID:      "v1.2.3",
		Role:           "bap",
		ExpectedRoot:   "root-expected",
		ComputedRoot:   "root-computed",
		BaselineDigest: "digest-1",
		Findings: []Finding{
			{ArtifactID: "config/onix.yaml", Kind: FindingModified, Details: []FindingDetail{
				{Path: "a.b.c", Message: `expected "x", got "secret"`},
			}},
			{ArtifactID: "policies/x.rego", Kind: FindingPolicy, Details: []FindingDetail{
				{Message: "policy says no: rule violated"},
			}},
		},
	}
}

// TestNewDeviationEvent verifies value stripping from modified findings,
// pass-through of policy details, and metadata propagation.
func TestNewDeviationEvent(t *testing.T) {
	report := deviationReport()
	loc := time.FixedZone("IST", 5*3600+1800)
	at := time.Date(2026, 7, 4, 12, 30, 0, 0, loc)

	event := NewDeviationEvent(report, at)

	if event.EventType != DeviationEventType {
		t.Fatalf("EventType = %q, want %q", event.EventType, DeviationEventType)
	}
	if !event.GeneratedAt.Equal(at) || event.GeneratedAt.Location() != time.UTC {
		t.Fatalf("GeneratedAt = %v, want UTC instant of %v", event.GeneratedAt, at)
	}
	if event.ExpectedRoot != report.ExpectedRoot || event.ComputedRoot != report.ComputedRoot {
		t.Fatalf("roots = (%q, %q), want (%q, %q)",
			event.ExpectedRoot, event.ComputedRoot, report.ExpectedRoot, report.ComputedRoot)
	}
	if event.NetworkID != "net-1" || event.DevkitID != "devkit-1" || event.Role != "bap" {
		t.Fatalf("metadata not copied: %+v", event)
	}

	if len(event.Findings) != 2 {
		t.Fatalf("Findings length = %d, want 2", len(event.Findings))
	}
	if got, want := event.Findings[0].Details, []FindingDetail{{Path: "a.b.c"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("modified finding details = %v, want %v (message stripped)", got, want)
	}
	if got, want := event.Findings[1].Details, []FindingDetail{{Message: "policy says no: rule violated"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("policy finding details = %v, want %v (unchanged)", got, want)
	}
}

// TestFindingDetailString verifies local rendering of structured details.
func TestFindingDetailString(t *testing.T) {
	tests := []struct {
		name   string
		detail FindingDetail
		want   string
	}{
		{name: "path and message", detail: FindingDetail{Path: "a.b", Message: "expected x, got y"}, want: "a.b: expected x, got y"},
		{name: "message only", detail: FindingDetail{Message: "policy denied"}, want: "policy denied"},
		{name: "path only", detail: FindingDetail{Path: "a.b"}, want: "a.b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.detail.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEmitDeviation verifies the collector POST: headers and payload on
// success (with no local values leaked), and errors on 500 and unreachable
// collectors.
func TestEmitDeviation(t *testing.T) {
	ctx := context.Background()
	event := NewDeviationEvent(deviationReport(), time.Now())

	t.Run("success posts sanitized json", func(t *testing.T) {
		var gotContentType string
		var gotBody []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotContentType = r.Header.Get("Content-Type")
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		if err := EmitDeviation(ctx, server.Client(), server.URL, event); err != nil {
			t.Fatalf("EmitDeviation() error: %v", err)
		}
		if gotContentType != "application/json" {
			t.Fatalf("Content-Type = %q, want %q", gotContentType, "application/json")
		}
		var received DeviationEvent
		if err := json.Unmarshal(gotBody, &received); err != nil {
			t.Fatalf("body did not unmarshal to DeviationEvent: %v", err)
		}
		if received.Role != "bap" {
			t.Fatalf("received Role = %q, want %q", received.Role, "bap")
		}
		if strings.Contains(string(gotBody), "secret") {
			t.Fatalf("serialized event leaked local value: %s", gotBody)
		}
	})

	t.Run("500 response is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer server.Close()

		err := EmitDeviation(ctx, server.Client(), server.URL, event)
		if err == nil {
			t.Fatal("EmitDeviation() = nil, want error for HTTP 500")
		}
		if !strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("EmitDeviation() error = %q, want it to contain %q", err, "HTTP 500")
		}
	})

	t.Run("unreachable collector is an error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := server.URL
		server.Close()

		if err := EmitDeviation(ctx, &http.Client{}, url, event); err == nil {
			t.Fatal("EmitDeviation() = nil, want error for unreachable collector")
		}
	})
}
