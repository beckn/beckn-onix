package catalogcrawler

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/telemetry"
	"github.com/beckn/catalog-core/pkg/catalog/crawler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Metric identity labels; pluginType matches the plugins.crawler config slot.
const (
	pluginID   = "catalogcrawler"
	pluginType = "crawler"
)

// Operation names, used as the operation metric label and, with
// spanNamePrefix, as the span name.
const (
	opPollTick       = "poll_tick"
	opSyncTick       = "sync_tick" // span only; its sync_next children carry the metrics
	opSyncNext       = "sync_next"
	opParkSweep      = "park_sweep"
	opRegistryLookup = "registry_lookup"
	opOnDemandCrawl  = "on_demand_crawl"
)

const spanNamePrefix = "catalogcrawler."

// Values for the status label.
const (
	statusSuccess        = "success"
	statusError          = "error"
	statusPartialFailure = "partial_failure" // some networks or sources failed and were skipped
	statusEmpty          = "empty"           // nothing to claim: the normal end of a drain
)

// Values for the error_type label. Kept to a fixed set (plus crawler.FaultClass)
// so the label stays low-cardinality; error details go on spans only.
const (
	errTypeLookupFailed     = "lookup_failed"
	errTypePollFailed       = "poll_failed"
	errTypePartialDiscovery = "partial_discovery"
	errTypeClaimFailed      = "claim_failed"
	errTypeSweepFailed      = "sweep_failed"
	errTypeCanceled         = "canceled"
	errTypeTimeout          = "timeout"
)

// Span-only attribute keys, too high-cardinality for metric labels.
const (
	attrNetworkID      = attribute.Key("network_id")
	attrNetworkIDs     = attribute.Key("network_ids")
	attrProvidersFound = attribute.Key("providers_found")
	attrRunID          = attribute.Key("run_id")
	attrClaimed        = attribute.Key("claimed")
	attrItemsClaimed   = attribute.Key("items_claimed")
)

func tracer() trace.Tracer {
	return otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
}

// operation is one instrumented unit of work: its span and start time.
type operation struct {
	name  string
	span  trace.Span
	start time.Time
}

func startOperation(ctx context.Context, op string, opts ...trace.SpanStartOption) (context.Context, *operation) {
	ctx, span := tracer().Start(ctx, spanNamePrefix+op, opts...)
	return ctx, &operation{name: op, span: span, start: time.Now()}
}

// end records the outcome on the span and in the plugin metrics, then ends the
// span. A non-empty errType also counts towards onix_plugin_errors_total.
func (o *operation) end(ctx context.Context, status, errType string, err error) {
	defer o.span.End()
	o.span.SetAttributes(telemetry.AttrStatus.String(status))
	if errType != "" {
		o.span.SetAttributes(telemetry.AttrErrorType.String(errType))
	}
	if err != nil {
		o.span.RecordError(err)
	}
	if status == statusError {
		desc := errType
		if err != nil {
			desc = err.Error()
		}
		o.span.SetStatus(codes.Error, desc)
	}
	recordOperation(ctx, o.name, status, errType, time.Since(o.start))
}

// endPoll ends a PollIndexes-driven operation. A partial discovery failure is
// still counted as an error, since the pass only used the healthy inputs.
func (o *operation) endPoll(ctx context.Context, err error, partial bool) {
	switch {
	case err != nil:
		o.end(ctx, statusError, classifyError(err, errTypePollFailed), err)
	case partial:
		o.end(ctx, statusPartialFailure, errTypePartialDiscovery, nil)
	default:
		o.end(ctx, statusSuccess, "", nil)
	}
}

// recordOperation fetches the metrics per call so they bind to whichever
// MeterProvider otelsetup registered. Telemetry errors never fail the crawl.
func recordOperation(ctx context.Context, op, status, errType string, d time.Duration) {
	m, err := telemetry.GetMetrics(ctx)
	if err != nil {
		return
	}
	attrs := []attribute.KeyValue{
		telemetry.AttrPluginID.String(pluginID),
		telemetry.AttrPluginType.String(pluginType),
		telemetry.AttrOperation.String(op),
		telemetry.AttrStatus.String(status),
	}
	m.PluginExecutionDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attrs...))
	if errType != "" {
		m.PluginErrorsTotal.Add(ctx, 1, metric.WithAttributes(append(attrs, telemetry.AttrErrorType.String(errType))...))
	}
}

// classifyError gives cancellation and deadline expiry their own error_type,
// so a shutdown does not look like a registry or store outage.
func classifyError(err error, fallback string) string {
	switch {
	case errors.Is(err, context.Canceled):
		return errTypeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return errTypeTimeout
	default:
		return fallback
	}
}

// syncErrorType classifies a claimed item's sync failure by catalog-core's
// fault class, the same split SyncNext uses to park or reschedule it.
func syncErrorType(err error) string {
	if t := classifyError(err, ""); t != "" {
		return t
	}
	return string(crawler.ClassifyFault(0, err)) // no HTTP status at this layer
}

// partialFailureKey carries a per-pass flag so the discovery layers inside
// PollIndexes can report a tolerated partial failure; PollIndexes itself only
// reports all-or-nothing.
type partialFailureKey struct{}

func withPartialFailureTracking(ctx context.Context) (context.Context, *atomic.Bool) {
	flag := &atomic.Bool{}
	return context.WithValue(ctx, partialFailureKey{}, flag), flag
}

// markPartialFailure flags the enclosing pass, if ctx tracks one, and adds an
// event to the current span.
func markPartialFailure(ctx context.Context, what string, failed, total int) {
	if flag, ok := ctx.Value(partialFailureKey{}).(*atomic.Bool); ok {
		flag.Store(true)
	}
	trace.SpanFromContext(ctx).AddEvent("partial discovery failure", trace.WithAttributes(
		attribute.String("what", what),
		attribute.Int("failed", failed),
		attribute.Int("total", total),
	))
}
