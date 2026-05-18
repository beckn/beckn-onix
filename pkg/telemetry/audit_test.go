package telemetry

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"go.opentelemetry.io/otel/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogsEnabled_DefaultFalse(t *testing.T) {
	// Ensure the flag starts false in a clean state.
	// We save and restore to avoid polluting other tests.
	original := LogsEnabled()
	t.Cleanup(func() { SetLogsEnabled(original) })

	SetLogsEnabled(false)
	assert.False(t, LogsEnabled(), "LogsEnabled should be false after SetLogsEnabled(false)")
}

func TestLogsEnabled_SetTrue(t *testing.T) {
	original := LogsEnabled()
	t.Cleanup(func() { SetLogsEnabled(original) })

	SetLogsEnabled(true)
	assert.True(t, LogsEnabled(), "LogsEnabled should be true after SetLogsEnabled(true)")
}

func TestEmitAuditLogs_Disabled(t *testing.T) {
	original := LogsEnabled()
	t.Cleanup(func() { SetLogsEnabled(original) })

	SetLogsEnabled(false)

	// Capture stdout to assert the disabled path emits no WARN.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	require.NotPanics(t, func() {
		EmitAuditLogs(context.Background(), []byte(`{"message":"test"}`))
	}, "EmitAuditLogs should not panic when logs are disabled")

	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	os.Stdout = oldStdout

	assert.NotContains(t, buf.String(), `"level":"warn"`,
		"disabled path must not emit a WARN log on every request")
}

func TestEmitAuditLogs_Enabled(t *testing.T) {
	ctx := context.Background()
	provider, exporter, err := NewTestProviderWithLogs(ctx)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	require.True(t, LogsEnabled(), "LogsEnabled should be true after NewTestProviderWithLogs")

	require.NotPanics(t, func() {
		EmitAuditLogs(ctx, []byte(`{"message":"audit-test"}`), log.String("extra_key", "extra_value"))
	}, "EmitAuditLogs should not panic when logs are enabled")

	// One log record must be emitted regardless of how selectAuditPayload
	// transforms the body (no matching audit rules → empty body string is fine).
	records := exporter.Records()
	require.Len(t, records, 1, "exactly one log record should be emitted")

	// Verify the standard attributes set by EmitAuditLogs are present.
	var hasChecksum, hasLogUUID, hasExtraKey bool
	records[0].WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case "checkSum":
			hasChecksum = true
		case "log_uuid":
			hasLogUUID = true
		case "extra_key":
			hasExtraKey = true
		}
		return true
	})
	assert.True(t, hasChecksum, "audit record should include checkSum attribute")
	assert.True(t, hasLogUUID, "audit record should include log_uuid attribute")
	assert.True(t, hasExtraKey, "audit record should include caller-supplied extra_key attribute")
}

func TestNewTestProviderWithLogs_ShutdownResetsFlag(t *testing.T) {
	ctx := context.Background()
	provider, _, err := NewTestProviderWithLogs(ctx)
	require.NoError(t, err)

	assert.True(t, LogsEnabled(), "LogsEnabled should be true while provider is active")

	require.NoError(t, provider.Shutdown(ctx))
	assert.False(t, LogsEnabled(), "LogsEnabled should be false after provider shutdown")
}
