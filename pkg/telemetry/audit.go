package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	logger "github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

const auditLoggerName = "Beckn_ONIX"

func EmitAuditLogs(ctx context.Context, body []byte, attrs ...log.KeyValue) {
	// global.GetLoggerProvider() always returns a no-op provider (never nil),
	// so a nil-check on the provider is ineffective. Instead we rely on the
	// logEnabled atomic flag, which otelsetup sets to true after calling
	// global.SetLoggerProvider with a real SDK provider. If logging was not
	// configured, we warn and return early rather than silently dropping records
	// into the no-op provider.
	if !LogsEnabled() {
		logger.Warnf(ctx, "failed to emit audit logs, logs disabled")
		return
	}

	provider := global.GetLoggerProvider()

	sum := sha256.Sum256(body)
	auditBody := selectAuditPayload(ctx, body)
	auditlog := provider.Logger(auditLoggerName)
	record := log.Record{}
	record.SetBody(log.StringValue(string(auditBody)))
	record.SetTimestamp(time.Now())
	record.SetObservedTimestamp(time.Now())
	record.SetSeverity(log.SeverityInfo)

	checkSum := hex.EncodeToString(sum[:])

	txnID, _ := ctx.Value(model.ContextKeyTxnID).(string)
	msgID, _ := ctx.Value(model.ContextKeyMsgID).(string)
	parentID, _ := ctx.Value(model.ContextKeyParentID).(string)

	record.AddAttributes(
		log.String("checkSum", checkSum),
		log.String("log_uuid", uuid.New().String()),
		log.String("transaction_id", txnID),
		log.String("message_id", msgID),
		log.String("parent_id", parentID),
	)

	if len(attrs) > 0 {
		record.AddAttributes(attrs...)
	}

	auditlog.Emit(ctx, record)
}
