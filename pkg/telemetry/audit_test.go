package telemetry

import (
	"context"
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

	originalDebugf := auditDebugf
	t.Cleanup(func() { auditDebugf = originalDebugf })

	var debugCalls int
	var gotMessage string
	auditDebugf = func(ctx context.Context, format string, v ...any) {
		debugCalls++
		gotMessage = format
		require.NotNil(t, ctx)
		require.Len(t, v, 0)
	}

	SetLogsEnabled(false)

	EmitAuditLogs(context.Background(), []byte(`{"message":"test"}`))

	require.Equal(t, 1, debugCalls, "EmitAuditLogs should emit one debug breadcrumb when logs are disabled")
	require.Equal(t, "audit logs disabled, skipping emit", gotMessage)
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
