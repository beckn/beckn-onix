package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/otelsetup"
	"github.com/beckn-one/beckn-onix/pkg/telemetry"
)

// metricsProvider implements the OtelSetupMetricsProvider interface for the otelsetup plugin.
type metricsProvider struct {
	impl otelsetup.Setup
}

// New creates a new telemetry provider instance.
func (m metricsProvider) New(ctx context.Context, config map[string]string) (*telemetry.Provider, func() error, error) {
	if ctx == nil {
		return nil, nil, errors.New("context cannot be nil")
	}

	// Convert map[string]string to otelsetup.Config
	telemetryConfig := &otelsetup.Config{
		ServiceName:    config["serviceName"],
		ServiceVersion: config["serviceVersion"],
		Environment:    config["environment"],
		Domain:         config["domain"],
		OtlpEndpoint:   config["otlpEndpoint"],
	}

	// to extract the device id from the parent id from context
	var deviceId string
	var producer string
	var producerType string
	var err error
	if v := ctx.Value(model.ContextKeyParentID); v != nil {
		parentID := v.(string)
		p := strings.Split(parentID, ":")
		deviceId = p[len(p)-1]
		producerType = p[0]
		producer = p[1]
	}

	if deviceId != "" {
		telemetryConfig.DeviceID = deviceId
	}

	if producer != "" {
		telemetryConfig.Producer = producer
	}
	if producerType != "" {
		telemetryConfig.ProducerType = producerType
	}

	// Parse enableTracing from config
	if enableTracingStr, ok := config["enableTracing"]; ok && enableTracingStr != "" {
		telemetryConfig.EnableTracing, err = strconv.ParseBool(enableTracingStr)
		if err != nil {
			log.Warnf(ctx, "Invalid enableTracing value: %s, defaulting to False", enableTracingStr)
		}
	}

	// Parse enableMetrics as boolean
	if enableMetricsStr, ok := config["enableMetrics"]; ok && enableMetricsStr != "" {
		telemetryConfig.EnableMetrics, err = strconv.ParseBool(enableMetricsStr)
		if err != nil {
			log.Warnf(ctx, "Invalid enableMetrics value '%s', defaulting to False: %v", enableMetricsStr, err)
		}
	}

	// Parse enableLogs as boolean
	if enableLogsStr, ok := config["enableLogs"]; ok && enableLogsStr != "" {
		telemetryConfig.EnableLogs, err = strconv.ParseBool(enableLogsStr)
		if err != nil {
			log.Warnf(ctx, "Invalid enableLogs value '%s', defaulting to False: %v", enableLogsStr, err)
		}
	}

	// Parse timeInterval as int
	if timeIntervalStr, ok := config["timeInterval"]; ok && timeIntervalStr != "" {
		telemetryConfig.TimeInterval, err = strconv.ParseInt(timeIntervalStr, 10, 64)
		if err != nil {
			log.Warnf(ctx, "Invalid timeInterval value: %s, defaulting to 5 second ", timeIntervalStr)
		}

	}

	// to set fields for audit logs
	if v, ok := config["auditFieldsConfig"]; ok && v != "" {
		if err := telemetry.LoadAuditFieldRules(ctx, v); err != nil {
			log.Warnf(ctx, "Failed to load audit field rules: %v", err)
		}
	}

	//to set network leval matric frequency and granularity
	if v, ok := config["networkMetricsGranularity"]; ok && v != "" {
		telemetry.SetNetworkMetricsConfig(v, "")
	}

	if v, ok := config["networkMetricsFrequency"]; ok && v != "" {
		telemetry.SetNetworkMetricsConfig("", v)
	}

	log.Debugf(ctx, "Telemetry config mapped: %+v", telemetryConfig)
	provider, err := m.impl.New(ctx, telemetryConfig)
	if err != nil {
		log.Errorf(ctx, err, "Failed to create telemetry provider instance")
		return nil, nil, err
	}

	// Wrap the Shutdown function to match the closer signature
	var closer func() error
	if provider != nil && provider.Shutdown != nil {
		closer = func() error {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return provider.Shutdown(shutdownCtx)
		}
	}

	log.Infof(ctx, "Telemetry provider instance created successfully")
	return provider, closer, nil
}

// Provider is the exported plugin instance
var Provider = metricsProvider{}
