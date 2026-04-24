package dispatcher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/retry"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

type scriptedProvider struct {
	mu      sync.Mutex
	script  []error
	idx     int
	results []*provider.Result
}

func (s *scriptedProvider) Name() string { return "pushover" }
func (s *scriptedProvider) Push(_ context.Context, _ *notificationpbv1.Notification) (*provider.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx < len(s.script) {
		err := s.script[s.idx]
		s.idx++
		if err != nil {
			return nil, err
		}
	}
	r := &provider.Result{ProviderResponse: "ok"}
	s.results = append(s.results, r)
	return r, nil
}

func TestRetrySucceedsAfterTransient(t *testing.T) {
	rec := &memRecorder{}
	te := &provider.TransientError{Err: errors.New("5xx")}
	sp := &scriptedProvider{script: []error{te, te, nil}} // fail twice then succeed

	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", sp),
		dispatcher.WithRecorder(rec),
		dispatcher.WithRetryPolicy(retry.NewPolicy(
			retry.WithMaxAttempts(5),
			retry.WithInitialBackoff(1*time.Millisecond),
			retry.WithMaxBackoff(10*time.Millisecond),
			retry.WithRNG(func() float64 { return 0 }), // no jitter sleep
		)),
		dispatcher.WithSleepFunc(func(_ context.Context, _ time.Duration) error { return nil }),
	)

	if err := d.Dispatch(context.Background(), pushoverNotif()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(rec.records) != 1 {
		t.Fatalf("records = %d", len(rec.records))
	}
	if rec.records[0].Status != outcomes.StatusDelivered {
		t.Errorf("status = %q", rec.records[0].Status)
	}
	if rec.records[0].Attempts != 3 {
		t.Errorf("attempts = %d (want 3)", rec.records[0].Attempts)
	}
}

func TestRetryExhausts(t *testing.T) {
	rec := &memRecorder{}
	te := &provider.TransientError{Err: errors.New("5xx")}
	sp := &scriptedProvider{script: []error{te, te, te, te, te}} // exhausts budget of 3

	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", sp),
		dispatcher.WithRecorder(rec),
		dispatcher.WithRetryPolicy(retry.NewPolicy(
			retry.WithMaxAttempts(3),
			retry.WithInitialBackoff(1*time.Millisecond),
			retry.WithMaxBackoff(10*time.Millisecond),
			retry.WithRNG(func() float64 { return 0 }),
		)),
		dispatcher.WithSleepFunc(func(_ context.Context, _ time.Duration) error { return nil }),
	)

	_ = d.Dispatch(context.Background(), pushoverNotif())
	if len(rec.records) != 1 {
		t.Fatalf("records = %d", len(rec.records))
	}
	o := rec.records[0]
	if o.Status != outcomes.StatusPermanentlyFailed {
		t.Errorf("status = %q", o.Status)
	}
	if o.Reason != outcomes.ReasonRetryBudgetExhausted {
		t.Errorf("reason = %q (want retry-budget-exhausted)", o.Reason)
	}
	if o.Attempts != 3 {
		t.Errorf("attempts = %d", o.Attempts)
	}
}

func TestRetryPermanentShortCircuits(t *testing.T) {
	rec := &memRecorder{}
	pe := &provider.PermanentError{Reason: outcomes.ReasonProviderRejectedRecipient, Err: errors.New("bad key")}
	sp := &scriptedProvider{script: []error{pe}}

	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", sp),
		dispatcher.WithRecorder(rec),
		dispatcher.WithRetryPolicy(retry.NewPolicy(retry.WithMaxAttempts(5))),
	)
	_ = d.Dispatch(context.Background(), pushoverNotif())
	if rec.records[0].Attempts != 1 {
		t.Errorf("permanent must terminate on attempt 1, got %d", rec.records[0].Attempts)
	}
	if rec.records[0].Reason != outcomes.ReasonProviderRejectedRecipient {
		t.Errorf("reason = %q", rec.records[0].Reason)
	}
}

func TestRetryContextCancelTerminates(t *testing.T) {
	rec := &memRecorder{}
	te := &provider.TransientError{Err: errors.New("5xx")}
	sp := &scriptedProvider{script: []error{te, te, te}}

	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", sp),
		dispatcher.WithRecorder(rec),
		dispatcher.WithRetryPolicy(retry.NewPolicy(
			retry.WithMaxAttempts(5),
			retry.WithInitialBackoff(100*time.Millisecond),
		)),
		dispatcher.WithSleepFunc(func(ctx context.Context, _ time.Duration) error {
			return ctx.Err()
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = d.Dispatch(ctx, pushoverNotif())
	if rec.records[0].Status != outcomes.StatusPermanentlyFailed {
		t.Errorf("status = %q", rec.records[0].Status)
	}
}
