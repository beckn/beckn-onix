// Deviation telemetry. When a verification finds deviations and the network
// manifest declares an observability collector, a compact JSON event is
// POSTed there so the facilitator sees configuration drift across the
// network. Events carry artifact IDs, deviation kinds, and deviating paths —
// never configuration values — so no participant data can leak through the
// telemetry channel.
package deployconform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DeviationEventType identifies deployment-deviation telemetry events.
const DeviationEventType = "deployment.deviation"

// DeviationEvent is the telemetry payload for one non-compliant role.
type DeviationEvent struct {
	EventType      string    `json:"eventType"`
	NetworkID      string    `json:"networkId"`
	DevkitID       string    `json:"devkitId"`
	ReleaseID      any       `json:"releaseId,omitempty"`
	Role           string    `json:"role"`
	ExpectedRoot   string    `json:"expectedRoot"`
	ComputedRoot   string    `json:"computedRoot"`
	BaselineDigest string    `json:"baselineDigest,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
	GeneratedAt    time.Time `json:"generatedAt"`
}

// NewDeviationEvent builds the telemetry event for a report, stripping local
// configuration values from modified-artifact findings: only the structured
// Path of each detail is kept, its Message (which renders local values and
// may include misplaced secrets) is dropped, so local values never leave the
// host while the facilitator still learns exactly which fields drifted.
// Policy violation messages are authored by the facilitator's own Rego and
// pass through unchanged.
func NewDeviationEvent(report *Report, generatedAt time.Time) DeviationEvent {
	findings := make([]Finding, 0, len(report.Findings))
	for _, f := range report.Findings {
		details := f.Details
		if f.Kind == FindingModified {
			details = make([]FindingDetail, 0, len(f.Details))
			for _, detail := range f.Details {
				details = append(details, FindingDetail{Path: detail.Path})
			}
		}
		findings = append(findings, Finding{ArtifactID: f.ArtifactID, Kind: f.Kind, Details: details})
	}
	return DeviationEvent{
		EventType:      DeviationEventType,
		NetworkID:      report.NetworkID,
		DevkitID:       report.DevkitID,
		ReleaseID:      report.ReleaseID,
		Role:           report.Role,
		ExpectedRoot:   report.ExpectedRoot,
		ComputedRoot:   report.ComputedRoot,
		BaselineDigest: report.BaselineDigest,
		Findings:       findings,
		GeneratedAt:    generatedAt.UTC(),
	}
}

// EmitDeviation POSTs one deviation event to the network's observability
// collector. Any non-2xx response is an error; the caller decides whether
// that is fatal (it normally is not — telemetry is best-effort).
func EmitDeviation(ctx context.Context, client *http.Client, collectorURL string, event DeviationEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode deviation event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, collectorURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build collector request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post deviation event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("collector returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
