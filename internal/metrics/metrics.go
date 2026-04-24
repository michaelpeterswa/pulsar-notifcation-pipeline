// Package metrics defines the application-level OpenTelemetry instruments.
// All custom metric names and their attribute schemas live here so that
// dashboards (docker/grafana/dashboards/pipeline.json) can be authored
// against a single source of truth.
//
// Instruments are lazy: the first call to a getter creates the instrument
// on the global MeterProvider. Before telemetry.Init runs, the global
// provider is a no-op, so tests and early startup are safe.
//
// Attribute keys follow OTel semantic conventions where applicable.
package metrics

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MeterName is the named meter every pipeline component registers against.
// Exported so tests can query the same meter provider if they need to.
const MeterName = "github.com/michaelpeterswa/pulsar-notifcation-pipeline"

// --- Metric names -----------------------------------------------------------

const (
	// Writer
	NameWriterSubmissions  = "pulsarnp.writer.submissions"         // counter
	NameWriterSubmitDur    = "pulsarnp.writer.submit.duration"     // histogram, seconds
	NameWriterPublishDur   = "pulsarnp.writer.publish.duration"    // histogram, seconds

	// Deliverer
	NameDelivererConsumed      = "pulsarnp.deliverer.consumed"           // counter
	NameDelivererOutcomes      = "pulsarnp.deliverer.outcomes"           // counter
	NameDelivererProviderDur   = "pulsarnp.deliverer.provider.duration"  // histogram, seconds
	NameDelivererAttempts      = "pulsarnp.deliverer.attempts"           // histogram, integer
	NameDelivererDecryptFailed = "pulsarnp.deliverer.decrypt_failures"   // counter (FR-016 alert target)
)

// --- Attribute keys ---------------------------------------------------------

const (
	AttrOutcome     = attribute.Key("outcome")          // accepted, validation_failed, auth_failed, substrate_unavailable, internal
	AttrService     = attribute.Key("service_name")     // originating upstream service (from bearer token)
	AttrStatus      = attribute.Key("status")           // delivered | permanently-failed | expired
	AttrReason      = attribute.Key("reason")           // reason code from data-model.md §5
	AttrProvider    = attribute.Key("provider")         // pushover | sandbox | ...
	AttrResultClass = attribute.Key("result_class")     // success | transient | permanent
)

// Outcome label values for the writer submissions counter.
const (
	OutcomeAccepted             = "accepted"
	OutcomeValidationFailed     = "validation_failed"
	OutcomeAuthFailed           = "auth_failed"
	OutcomeSubstrateUnavailable = "substrate_unavailable"
	OutcomeInternal             = "internal"
)

// Provider-call result classifications for the deliverer provider.duration.
const (
	ResultSuccess   = "success"
	ResultTransient = "transient"
	ResultPermanent = "permanent"
)

// --- Instrument accessors ---------------------------------------------------

var (
	initOnce sync.Once

	writerSubmissions  metric.Int64Counter
	writerSubmitDur    metric.Float64Histogram
	writerPublishDur   metric.Float64Histogram

	delivererConsumed      metric.Int64Counter
	delivererOutcomes      metric.Int64Counter
	delivererProviderDur   metric.Float64Histogram
	delivererAttempts      metric.Int64Histogram
	delivererDecryptFailed metric.Int64Counter
)

func initInstruments() {
	initOnce.Do(func() {
		m := otel.Meter(MeterName)

		writerSubmissions, _ = m.Int64Counter(NameWriterSubmissions,
			metric.WithDescription("Total notification submissions accepted or rejected by the writer, labelled by outcome."))
		writerSubmitDur, _ = m.Float64Histogram(NameWriterSubmitDur,
			metric.WithDescription("Writer submit-handler latency (request received → response emitted), in seconds."),
			metric.WithUnit("s"))
		writerPublishDur, _ = m.Float64Histogram(NameWriterPublishDur,
			metric.WithDescription("Latency of the writer's Pulsar publish call only, in seconds."),
			metric.WithUnit("s"))

		delivererConsumed, _ = m.Int64Counter(NameDelivererConsumed,
			metric.WithDescription("Total messages consumed from the Pulsar topic by the deliverer."))
		delivererOutcomes, _ = m.Int64Counter(NameDelivererOutcomes,
			metric.WithDescription("Terminal delivery outcomes per notification, labelled by status+reason+provider."))
		delivererProviderDur, _ = m.Float64Histogram(NameDelivererProviderDur,
			metric.WithDescription("Push-provider round-trip latency per attempt, labelled by provider+result_class."),
			metric.WithUnit("s"))
		delivererAttempts, _ = m.Int64Histogram(NameDelivererAttempts,
			metric.WithDescription("Number of provider attempts made per notification before reaching a terminal outcome."))
		delivererDecryptFailed, _ = m.Int64Counter(NameDelivererDecryptFailed,
			metric.WithDescription("Total encryption-policy-violation terminal outcomes (FR-016). An alert target."))
	})
}

// --- Writer helpers ---------------------------------------------------------

// RecordWriterSubmission increments the submissions counter and records
// the submit-handler latency, both tagged with outcome and service_name.
func RecordWriterSubmission(ctx context.Context, outcome, serviceName string, durationSec float64) {
	initInstruments()
	attrs := metric.WithAttributes(
		AttrOutcome.String(outcome),
		AttrService.String(serviceName),
	)
	writerSubmissions.Add(ctx, 1, attrs)
	writerSubmitDur.Record(ctx, durationSec, attrs)
}

// RecordPublishDuration records the Pulsar publish-call latency.
func RecordPublishDuration(ctx context.Context, durationSec float64) {
	initInstruments()
	writerPublishDur.Record(ctx, durationSec)
}

// --- Deliverer helpers ------------------------------------------------------

// RecordConsumed increments the total-consumed counter.
func RecordConsumed(ctx context.Context) {
	initInstruments()
	delivererConsumed.Add(ctx, 1)
}

// RecordOutcome records one terminal outcome.
func RecordOutcome(ctx context.Context, status, reason, provider string, attempts int) {
	initInstruments()
	attrs := metric.WithAttributes(
		AttrStatus.String(status),
		AttrReason.String(reason),
		AttrProvider.String(provider),
	)
	delivererOutcomes.Add(ctx, 1, attrs)
	if attempts > 0 {
		delivererAttempts.Record(ctx, int64(attempts), metric.WithAttributes(
			AttrStatus.String(status),
			AttrProvider.String(provider),
		))
	}
	if reason == "encryption-policy-violation" {
		delivererDecryptFailed.Add(ctx, 1)
	}
}

// RecordProviderAttempt records one provider round-trip.
func RecordProviderAttempt(ctx context.Context, provider, resultClass string, durationSec float64) {
	initInstruments()
	delivererProviderDur.Record(ctx, durationSec, metric.WithAttributes(
		AttrProvider.String(provider),
		AttrResultClass.String(resultClass),
	))
}
