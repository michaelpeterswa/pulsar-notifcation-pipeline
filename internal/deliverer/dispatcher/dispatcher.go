// Package dispatcher owns the per-message control flow inside the deliverer:
// decide which PushProvider handles the notification, invoke it, classify the
// result, and record the terminal outcome.
package dispatcher

import (
	"context"
	"errors"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/retry"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/metrics"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// Dispatcher routes a consumed Notification to the registered PushProvider
// and applies the retry policy on transient failures (FR-009).
type Dispatcher struct {
	providers map[string]provider.PushProvider
	recorder  outcomes.Recorder
	clock     func() time.Time
	retry     *retry.Policy
	sleep     func(context.Context, time.Duration) error
}

// Option is the functional option for New.
type Option func(*Dispatcher)

// WithProvider registers a PushProvider under the oneof-arm name it handles
// (e.g. "pushover"). Calling New requires at least one provider.
func WithProvider(armName string, p provider.PushProvider) Option {
	return func(d *Dispatcher) { d.providers[armName] = p }
}

// WithRecorder supplies the outcomes.Recorder. Required.
func WithRecorder(r outcomes.Recorder) Option { return func(d *Dispatcher) { d.recorder = r } }

// WithClock overrides the time source (test-only).
func WithClock(fn func() time.Time) Option { return func(d *Dispatcher) { d.clock = fn } }

// WithRetryPolicy overrides the retry policy. Default: 1 attempt (no retry —
// US1 behaviour preserved when callers do not opt into retry). Callers that
// want resilience (US2) construct the dispatcher with a real policy.
func WithRetryPolicy(p *retry.Policy) Option { return func(d *Dispatcher) { d.retry = p } }

// WithSleepFunc overrides the backoff-sleep function. Default:
// context-aware time.Sleep. Test-only.
func WithSleepFunc(fn func(context.Context, time.Duration) error) Option {
	return func(d *Dispatcher) { d.sleep = fn }
}

// New constructs a Dispatcher.
func New(opts ...Option) (*Dispatcher, error) {
	d := &Dispatcher{
		providers: map[string]provider.PushProvider{},
		clock:     time.Now,
		retry:     retry.NewPolicy(retry.WithMaxAttempts(1)),
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return nil
			}
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	for _, opt := range opts {
		opt(d)
	}
	if len(d.providers) == 0 {
		return nil, errors.New("dispatcher: at least one WithProvider is required")
	}
	if d.recorder == nil {
		return nil, errors.New("dispatcher: WithRecorder is required")
	}
	return d, nil
}

// Dispatch processes n and emits exactly one terminal outcome. Returns nil
// on success (delivered or intentionally terminal); returns an error only if
// the provider registry lookup itself failed.
func (d *Dispatcher) Dispatch(ctx context.Context, n *notificationpbv1.Notification) error {
	now := d.clock().UTC()
	if n == nil {
		return errors.New("dispatcher: nil notification")
	}

	if expired, exp := isExpired(n, now); expired {
		d.recorder.Record(ctx, outcomes.Outcome{
			NotificationID: n.GetNotificationId(),
			Status:         outcomes.StatusExpired,
			Reason:         outcomes.ReasonExpired,
			Attempts:       0,
			FirstAttempt:   exp,
			LastAttempt:    exp,
		})
		metrics.RecordOutcome(ctx, string(outcomes.StatusExpired), outcomes.ReasonExpired, "", 0)
		return nil
	}

	p, _, err := d.resolve(n)
	if err != nil {
		// Unknown oneof arm → terminal payload rejection.
		d.recorder.Record(ctx, outcomes.Outcome{
			NotificationID: n.GetNotificationId(),
			Status:         outcomes.StatusPermanentlyFailed,
			Reason:         outcomes.ReasonProviderRejectedPayload,
			Attempts:       0,
			FirstAttempt:   now,
			LastAttempt:    now,
		})
		metrics.RecordOutcome(ctx, string(outcomes.StatusPermanentlyFailed),
			outcomes.ReasonProviderRejectedPayload, "", 0)
		return nil
	}

	// Retry loop. Each iteration is one attempt; on transient errors we back
	// off via the policy until the budget is exhausted; on permanent errors
	// we terminate immediately.
	iter := d.retry.NewIterator()
	var first, last time.Time
	var lastErr error
	var lastResp string

	for !iter.Done() {
		backoff := iter.Next()
		if iter.Attempt() > 1 {
			if err := d.sleep(ctx, backoff); err != nil {
				// Context cancelled mid-backoff — terminate with whatever
				// we know about the last attempt.
				d.recorder.Record(ctx, outcomes.Outcome{
					NotificationID:   n.GetNotificationId(),
					Status:           outcomes.StatusPermanentlyFailed,
					Reason:           outcomes.ReasonRetryBudgetExhausted,
					ProviderResponse: lastResp,
					Attempts:         iter.Attempt() - 1,
					FirstAttempt:     first,
					LastAttempt:      last,
				})
				metrics.RecordOutcome(ctx, string(outcomes.StatusPermanentlyFailed),
					outcomes.ReasonRetryBudgetExhausted, p.Name(), iter.Attempt()-1)
				return nil
			}
		}

		attemptStart := d.clock().UTC()
		if first.IsZero() {
			first = attemptStart
		}
		res, err := p.Push(ctx, n)
		last = d.clock().UTC()
		attemptDur := last.Sub(attemptStart).Seconds()

		if err == nil {
			metrics.RecordProviderAttempt(ctx, p.Name(), metrics.ResultSuccess, attemptDur)
			d.recorder.Record(ctx, outcomes.Outcome{
				NotificationID:   n.GetNotificationId(),
				Status:           outcomes.StatusDelivered,
				ProviderResponse: safeResponse(res),
				Attempts:         iter.Attempt(),
				FirstAttempt:     first,
				LastAttempt:      last,
			})
			metrics.RecordOutcome(ctx, string(outcomes.StatusDelivered), "", p.Name(), iter.Attempt())
			return nil
		}
		lastErr = err

		var pe *provider.PermanentError
		if errors.As(err, &pe) {
			metrics.RecordProviderAttempt(ctx, p.Name(), metrics.ResultPermanent, attemptDur)
			d.recorder.Record(ctx, outcomes.Outcome{
				NotificationID:   n.GetNotificationId(),
				Status:           outcomes.StatusPermanentlyFailed,
				Reason:           pe.Reason,
				ProviderResponse: pe.Response,
				Attempts:         iter.Attempt(),
				FirstAttempt:     first,
				LastAttempt:      last,
			})
			metrics.RecordOutcome(ctx, string(outcomes.StatusPermanentlyFailed), pe.Reason, p.Name(), iter.Attempt())
			return nil
		}
		var te *provider.TransientError
		if errors.As(err, &te) {
			metrics.RecordProviderAttempt(ctx, p.Name(), metrics.ResultTransient, attemptDur)
			lastResp = te.Response
			// Keep retrying until iter.Done().
			continue
		}
		metrics.RecordProviderAttempt(ctx, p.Name(), metrics.ResultTransient, attemptDur)
		// Unknown error shape; treat as transient-equivalent and retry.
		lastResp = err.Error()
	}

	// Retry budget exhausted on transient failures.
	d.recorder.Record(ctx, outcomes.Outcome{
		NotificationID:   n.GetNotificationId(),
		Status:           outcomes.StatusPermanentlyFailed,
		Reason:           outcomes.ReasonRetryBudgetExhausted,
		ProviderResponse: lastResp,
		Attempts:         iter.Attempt(),
		FirstAttempt:     first,
		LastAttempt:      last,
	})
	metrics.RecordOutcome(ctx, string(outcomes.StatusPermanentlyFailed),
		outcomes.ReasonRetryBudgetExhausted, p.Name(), iter.Attempt())
	_ = lastErr // retained for future log enrichment; not fatal here.
	return nil
}

func (d *Dispatcher) resolve(n *notificationpbv1.Notification) (provider.PushProvider, string, error) {
	switch n.GetTarget().(type) {
	case *notificationpbv1.Notification_Pushover:
		if p, ok := d.providers["pushover"]; ok {
			return p, "pushover", nil
		}
		return nil, "pushover", &provider.NotImplementedError{Target: "pushover"}
	default:
		return nil, "unknown", &provider.NotImplementedError{Target: "unknown"}
	}
}

func isExpired(n *notificationpbv1.Notification, now time.Time) (bool, time.Time) {
	if n.GetExpiresAt() == nil {
		return false, time.Time{}
	}
	exp := n.GetExpiresAt().AsTime()
	if exp.IsZero() {
		return false, time.Time{}
	}
	return now.After(exp), now
}

func safeResponse(r *provider.Result) string {
	if r == nil {
		return ""
	}
	return r.ProviderResponse
}
