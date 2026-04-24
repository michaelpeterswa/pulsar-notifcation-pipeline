// Package telemetry wires OpenTelemetry providers via alpineworks.io/ootel
// according to the shared env configuration (Principle V).
//
//	Local=true   → OTLP gRPC export of both metrics and traces. Matches the
//	              local docker-compose LGTM stack (OTEL_EXPORTER_OTLP_ENDPOINT
//	              points at the lgtm container).
//	Local=false  → Prometheus scrape endpoint on MetricsPort for production
//	              metrics; traces still push via OTLP gRPC if TracingEnabled.
//
// In addition to the application-level instruments registered by
// internal/metrics, this package turns on Go runtime metrics (GC, goroutines,
// memstats) and host metrics (CPU, memory) so dashboards have meaningful
// resource-utilization panels without per-app wiring.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"alpineworks.io/ootel"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

// Shutdown is returned by Init. It flushes and closes every registered
// provider. Safe to call once.
type Shutdown func(context.Context) error

// Config is the subset of the shared env config this package needs.
// Supplied by the caller to avoid an import cycle with internal/config.
type Config struct {
	ServiceName    string
	ServiceVersion string

	MetricsEnabled bool
	MetricsPort    int

	TracingEnabled    bool
	TracingSampleRate float64

	// Local flips the metric exporter between OTLP gRPC (true — local
	// docker-compose stack pushes to lgtm:4317) and Prometheus scrape
	// (false — scraped by an external Prometheus-compatible collector).
	Local bool
}

// Init builds and registers the OTel providers via ootel. Returns a Shutdown
// closure the caller MUST invoke during graceful shutdown.
func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if cfg.ServiceName == "" {
		return nil, errors.New("telemetry: ServiceName is required")
	}

	exporter := ootel.ExporterTypePrometheus
	if cfg.Local {
		exporter = ootel.ExporterTypeOTLPGRPC
	}

	client := ootel.NewOotelClient(
		ootel.WithMetricConfig(
			ootel.NewMetricConfig(cfg.MetricsEnabled, exporter, cfg.MetricsPort),
		),
		ootel.WithTraceConfig(
			ootel.NewTraceConfig(cfg.TracingEnabled, cfg.TracingSampleRate, cfg.ServiceName, cfg.ServiceVersion),
		),
	)

	shutdown, err := client.Init(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: ootel init: %w", err)
	}

	// Runtime + host metrics are registered once on the global meter
	// provider ootel installed. Disable them for unit tests by leaving
	// MetricsEnabled=false.
	if cfg.MetricsEnabled {
		if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(5 * time.Second)); err != nil {
			_ = shutdown(ctx)
			return nil, fmt.Errorf("telemetry: runtime metrics: %w", err)
		}
		if err := host.Start(); err != nil {
			_ = shutdown(ctx)
			return nil, fmt.Errorf("telemetry: host metrics: %w", err)
		}
	}

	return shutdown, nil
}
