package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	applog "github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/google/uuid"
	auditlog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

const auditLoggerName = "Beckn_ONIX"

// auditDebugf is injected so tests can assert on the disabled path without
// relying on the global log sink.
var auditDebugf = applog.Debugf

func EmitAuditLogs(ctx context.Context, body []byte, attrs ...auditlog.KeyValue) {
	// global.GetLoggerProvider() always returns a no-op provider (never nil),
	// so a nil-check on the provider is ineffective. Instead we rely on the
	// logEnabled atomic flag, which otelsetup sets to true after calling
	// global.SetLoggerProvider with a real SDK provider. If logging was not
	// configured, we emit a debug breadcrumb and return early rather than
	// emitting a noisy warning on every request.
	if !LogsEnabled() {
		auditDebugf(ctx, "audit logs disabled, skipping emit")
		return
	}

	provider := global.GetLoggerProvider()

	sum := sha256.Sum256(body)
	auditBody := ProcessAuditPayload(ctx, body)
	otelLogger := provider.Logger(auditLoggerName)
	record := auditlog.Record{}
	record.SetBody(auditlog.StringValue(string(auditBody)))
	record.SetTimestamp(time.Now())
	record.SetObservedTimestamp(time.Now())
	record.SetSeverity(auditlog.SeverityInfo)

	checkSum := hex.EncodeToString(sum[:])

	txnID, _ := ctx.Value(model.ContextKeyTxnID).(string)
	msgID, _ := ctx.Value(model.ContextKeyMsgID).(string)
	parentID, _ := ctx.Value(model.ContextKeyParentID).(string)

	record.AddAttributes(
		auditlog.String("checkSum", checkSum),
		auditlog.String("log_uuid", uuid.New().String()),
		auditlog.String("transaction_id", txnID),
		auditlog.String("message_id", msgID),
		auditlog.String("parent_id", parentID),
	)

	if len(attrs) > 0 {
		record.AddAttributes(attrs...)
	}

	otelLogger.Emit(ctx, record)
}
