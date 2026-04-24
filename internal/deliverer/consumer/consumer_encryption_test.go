package consumer_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
)

// oncePolicyViolationReader surfaces ErrEncryptionPolicyViolation a fixed
// number of times, then yields ctx.Done. This exercises FR-016 consumer
// behaviour: the consumer MUST emit a terminal outcome and MUST NOT dispatch.
type oncePolicyViolationReader struct {
	mu   sync.Mutex
	left int
}

func (r *oncePolicyViolationReader) Receive(ctx context.Context) (*pulsarlib.ConsumedMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.left > 0 {
		r.left--
		return nil, pulsarlib.ErrEncryptionPolicyViolation
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r *oncePolicyViolationReader) Close() error { return nil }

func TestConsumerEmitsEncryptionPolicyViolation(t *testing.T) {
	reader := &oncePolicyViolationReader{left: 3}
	sb := sandbox.New()
	rec := &memRecorder{}
	disp, err := dispatcher.New(
		dispatcher.WithProvider("pushover", sb),
		dispatcher.WithRecorder(rec),
	)
	if err != nil {
		t.Fatal(err)
	}
	c, err := consumer.New(
		consumer.WithReader(reader),
		consumer.WithDispatcher(disp),
		consumer.WithRecorder(rec),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.Run(ctx)

	violations := 0
	for _, o := range rec.records {
		if o.Reason == outcomes.ReasonEncryptionPolicyViolation {
			violations++
			if o.Status != outcomes.StatusPermanentlyFailed {
				t.Errorf("policy-violation outcome has wrong status: %q", o.Status)
			}
		}
	}
	if violations != 3 {
		t.Errorf("expected 3 encryption-policy-violation records, got %d (all: %+v)", violations, rec.records)
	}
	if len(sb.Delivered()) != 0 {
		t.Errorf("sandbox was called %d times; FR-016 forbids any dispatch", len(sb.Delivered()))
	}
}

// TestConsumerPoisonPillDoesNotBlock confirms that other transient reader
// errors don't silently loop — they surface to the caller for handling.
func TestConsumerReaderErrorSurfaces(t *testing.T) {
	reader := &scriptedReader{queue: []any{fmt.Errorf("boom")}}
	sb := sandbox.New()
	rec := &memRecorder{}
	disp, _ := dispatcher.New(dispatcher.WithProvider("pushover", sb), dispatcher.WithRecorder(rec))
	c, _ := consumer.New(
		consumer.WithReader(reader),
		consumer.WithDispatcher(disp),
		consumer.WithRecorder(rec),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.Run(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("consumer swallowed reader error; got %v", err)
	}
}
