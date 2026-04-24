package retry_test

import (
	"errors"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/retry"
)

func TestClassifyTransient(t *testing.T) {
	te := &provider.TransientError{Err: errors.New("5xx")}
	if got := retry.Classify(te); got != retry.ClassTransient {
		t.Errorf("got %v", got)
	}
}

func TestClassifyPermanent(t *testing.T) {
	pe := &provider.PermanentError{Reason: "provider-rejected-payload"}
	if got := retry.Classify(pe); got != retry.ClassPermanent {
		t.Errorf("got %v", got)
	}
}

func TestClassifyUnknown(t *testing.T) {
	// Unknown errors are treated as transient (conservative).
	if got := retry.Classify(errors.New("surprise")); got != retry.ClassTransient {
		t.Errorf("got %v", got)
	}
}

func TestIteratorExhausts(t *testing.T) {
	p := retry.NewPolicy(
		retry.WithMaxAttempts(3),
		retry.WithInitialBackoff(10*time.Millisecond),
		retry.WithMaxBackoff(100*time.Millisecond),
		retry.WithRNG(func() float64 { return 1.0 }),
	)
	it := p.NewIterator()
	attempts := 0
	for !it.Done() {
		_ = it.Next()
		attempts++
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if it.Attempt() != 3 {
		t.Errorf("Attempt() = %d", it.Attempt())
	}
}

func TestIteratorBackoffGrowsBounded(t *testing.T) {
	p := retry.NewPolicy(
		retry.WithMaxAttempts(10),
		retry.WithInitialBackoff(10*time.Millisecond),
		retry.WithMaxBackoff(80*time.Millisecond),
		retry.WithRNG(func() float64 { return 1.0 }),
	)
	it := p.NewIterator()
	got := []time.Duration{it.Next(), it.Next(), it.Next(), it.Next(), it.Next(), it.Next()}
	want := []time.Duration{
		10 * time.Millisecond, // attempt 1 → initial
		20 * time.Millisecond, // attempt 2 → 2x
		40 * time.Millisecond, // attempt 3 → 4x
		80 * time.Millisecond, // attempt 4 → capped at max
		80 * time.Millisecond, // attempt 5 → still capped
		80 * time.Millisecond, // attempt 6 → still capped
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestIteratorJitter(t *testing.T) {
	// rng=0 → every step returns zero duration.
	p := retry.NewPolicy(
		retry.WithMaxAttempts(3),
		retry.WithInitialBackoff(time.Second),
		retry.WithMaxBackoff(time.Minute),
		retry.WithRNG(func() float64 { return 0 }),
	)
	it := p.NewIterator()
	for i := 0; i < 3; i++ {
		if d := it.Next(); d != 0 {
			t.Errorf("step %d: rng=0 should yield 0, got %v", i, d)
		}
	}
}
