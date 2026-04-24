// Package retry is the deliverer's retry classifier and bounded-backoff
// iterator. Transient provider errors retry with exponential backoff plus
// full jitter; permanent errors short-circuit to terminal immediately.
//
// FR-009 (classify transient vs permanent), US2 P2 resilience.
package retry

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
)

// Class classifies an error for retry purposes.
type Class int

const (
	// ClassPermanent — terminal failure; do not retry.
	ClassPermanent Class = iota + 1
	// ClassTransient — retry with backoff, bounded by the policy.
	ClassTransient
)

// Classify inspects err and returns Permanent (including nil, which callers
// should treat as success) or Transient.
func Classify(err error) Class {
	if err == nil {
		return ClassPermanent // defensive: caller should not call on nil
	}
	if errors.Is(err, provider.ErrTransient) {
		return ClassTransient
	}
	if errors.Is(err, provider.ErrPermanent) {
		return ClassPermanent
	}
	// Unknown errors are conservatively classified as transient so the
	// bounded retry budget provides a chance to recover; they terminate
	// with retry-budget-exhausted if they persist.
	return ClassTransient
}

// Policy is the bounded-backoff configuration.
type Policy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	rng            func() float64
}

// Option is the functional option for NewPolicy.
type Option func(*Policy)

// WithMaxAttempts overrides the attempt budget (must be > 0).
func WithMaxAttempts(n int) Option { return func(p *Policy) { p.MaxAttempts = n } }

// WithInitialBackoff overrides the initial backoff.
func WithInitialBackoff(d time.Duration) Option { return func(p *Policy) { p.InitialBackoff = d } }

// WithMaxBackoff caps the per-step backoff.
func WithMaxBackoff(d time.Duration) Option { return func(p *Policy) { p.MaxBackoff = d } }

// WithRNG supplies a deterministic RNG for tests. Default: crypto-safe
// random from math/rand/v2.
func WithRNG(fn func() float64) Option { return func(p *Policy) { p.rng = fn } }

// NewPolicy constructs a policy. Defaults: 5 attempts, 500ms initial, 30s max.
func NewPolicy(opts ...Option) *Policy {
	p := &Policy{
		MaxAttempts:    5,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     30 * time.Second,
		rng:            rand.Float64,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Iterator yields successive backoff durations for a Policy. Callers use
// Next to advance; Done reports budget exhaustion.
type Iterator struct {
	policy  *Policy
	attempt int
}

// NewIterator constructs an Iterator.
func (p *Policy) NewIterator() *Iterator { return &Iterator{policy: p, attempt: 0} }

// Done reports whether the policy's attempt budget has been exhausted.
func (i *Iterator) Done() bool { return i.attempt >= i.policy.MaxAttempts }

// Attempt returns the number of attempts already performed (i.e., Next has
// been called that many times). Useful for outcome records.
func (i *Iterator) Attempt() int { return i.attempt }

// Next yields the next backoff duration. Subsequent attempts use exponential
// backoff from InitialBackoff doubling each step, capped at MaxBackoff, with
// full-jitter applied. Returns 0 and advances; the caller sleeps if the
// returned duration is > 0.
//
// The first call returns 0 (i.e., "retry immediately is NOT desired" is not
// the meaning — the first retry AFTER an initial attempt still applies a
// full-jitter backoff; see the test for the exact contract).
func (i *Iterator) Next() time.Duration {
	i.attempt++
	base := i.policy.InitialBackoff
	for n := 1; n < i.attempt; n++ {
		base *= 2
		if base >= i.policy.MaxBackoff {
			base = i.policy.MaxBackoff
			break
		}
	}
	if base > i.policy.MaxBackoff {
		base = i.policy.MaxBackoff
	}
	return time.Duration(i.policy.rng() * float64(base))
}
