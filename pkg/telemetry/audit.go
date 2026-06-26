package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	logger "github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

const auditLoggerName = "Beckn_ONIX"

func EmitAuditLogs(ctx context.Context, body []byte, header http.Header, attrs ...log.KeyValue) {
	// global.GetLoggerProvider() always returns a no-op provider (never nil),
	// so a nil-check on the provider is ineffective. Instead we rely on the
	// logEnabled atomic flag, which otelsetup sets to true after calling
	// global.SetLoggerProvider with a real SDK provider. Observability is
	// optional, so a disabled logs path is expected in production — emit a
	// debug breadcrumb rather than a warning to avoid per-request WARN noise.
	if !LogsEnabled() {
		logger.Debugf(ctx, "audit logs disabled, skipping emit")
		return
	}

	provider := global.GetLoggerProvider()

	sum := sha256.Sum256(body)
	auditBody := ProcessAuditPayload(ctx, body)
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

	// cfg was already read above via ProcessAuditPayload → GetCompiledConfig, but we
	// re-read here to avoid threading the value through. The RWMutex is uncontended
	// on the read path so the cost is negligible; if that changes, snapshot once at
	// the top of this function instead.
	if cfg := GetCompiledConfig(); cfg != nil && cfg.CaptureSignatureHeaders() && header != nil {
		for _, name := range signatureHeaders {
			if val := header.Get(name); val != "" {
				// strings.ToLower is intentional: OTel semantic conventions require
				// header attribute keys to be lowercase (e.g. "x-request-id").
				// header.Get uses canonical MIME casing internally, so lookup is
				// case-insensitive regardless of how name is cased in signatureHeaders.
				record.AddAttributes(log.String("http.request.header."+strings.ToLower(name), val))
			}
		}
	}

	auditlog.Emit(ctx, record)
}
