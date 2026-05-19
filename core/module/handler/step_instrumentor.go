package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

// StepRunner represents the minimal contract required for step instrumentation.
type StepRunner interface {
	Run(*model.StepContext) error
}

// InstrumentedStep wraps a processing step with telemetry instrumentation.
type InstrumentedStep struct {
	step       StepRunner
	stepName   string
	moduleName string
	metrics    *StepMetrics
}

// NewInstrumentedStep returns a telemetry enabled wrapper around a definition.Step.
func NewInstrumentedStep(step StepRunner, stepName, moduleName string) (*InstrumentedStep, error) {
	metrics, err := GetStepMetrics(context.Background())
	if err != nil {
		return nil, err
	}

	return &InstrumentedStep{
		step:       step,
		stepName:   stepName,
		moduleName: moduleName,
		metrics:    metrics,
	}, nil
}

type becknError interface {
	BecknError() *model.Error
}

// Run executes the underlying step and records RED style metrics.
func (is *InstrumentedStep) Run(ctx *model.StepContext) error {
	if is.metrics == nil {
		return is.step.Run(ctx)
	}

	tracer := otel.Tracer(telemetry.ScopeName, trace.WithInstrumentationVersion(telemetry.ScopeVersion))
	stepName := "step:" + is.stepName
	spanCtx, span := tracer.Start(ctx.Context, stepName)
	defer span.End()

	// Shallow-copy the entire StepContext so new fields are carried in
	// automatically, then replace only the embedded context with the span context.
	// This prevents silent breakage when new fields are added to StepContext.
	stepCtx := *ctx
	stepCtx.Context = spanCtx

	start := time.Now()
	err := is.step.Run(&stepCtx)
	duration := time.Since(start).Seconds()

	attrs := []attribute.KeyValue{
		telemetry.AttrModule.String(is.moduleName),
		telemetry.AttrStep.String(is.stepName),
		telemetry.AttrRole.String(string(stepCtx.Role)),
	}

	is.metrics.StepExecutionTotal.Add(stepCtx.Context, 1, metric.WithAttributes(attrs...))
	is.metrics.StepExecutionDuration.Record(stepCtx.Context, duration, metric.WithAttributes(attrs...))

	if err != nil {
		errorType := fmt.Sprintf("%T", err)
		var becknErr becknError
		if errors.As(err, &becknErr) {
			if be := becknErr.BecknError(); be != nil && be.Code != "" {
				errorType = be.Code
			}
		}

		errorAttrs := append(attrs, telemetry.AttrErrorType.String(errorType))
		is.metrics.StepErrorsTotal.Add(stepCtx.Context, 1, metric.WithAttributes(errorAttrs...))
		log.Errorf(stepCtx.Context, err, "Step %s failed", is.stepName)
	}

	// Write back fields that steps are permitted to mutate during Run.
	// ProtocolVersion is read-only — steps must not change it.
	if stepCtx.Route != nil {
		ctx.Route = stepCtx.Route
	}
	ctx.WithContext(stepCtx.Context)
	return err
}
