package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/telemetry"
)

func TestInitRequiresServiceName(t *testing.T) {
	if _, err := telemetry.Init(context.Background(), telemetry.Config{}); err == nil {
		t.Fatal("expected missing ServiceName error")
	}
}

func TestInitDisabledIsNoOp(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName:    "t-test",
		ServiceVersion: "0.0.1",
		MetricsEnabled: false,
		TracingEnabled: false,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Shutdown is nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// The Local=false / Prometheus-exposed path binds an HTTP listener and is
// exercised by the docker-compose integration, not by this unit test.
// The Local=true / OTLP path requires a reachable collector and is likewise
// exercised end-to-end by the compose stack.
