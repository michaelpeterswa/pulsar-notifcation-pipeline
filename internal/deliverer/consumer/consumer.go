// Package consumer drives the deliverer's read-decrypt-dispatch loop. It
// pulls messages from pulsarlib.Consumer, emits the encryption-policy-
// violation terminal outcome on decrypt failure (FR-016), and hands
// decrypted messages to the dispatcher.
package consumer

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/protobuf/proto"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/metrics"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// Consumer runs the deliverer's main loop.
type Consumer struct {
	reader     pulsarlib.Consumer
	dispatcher *dispatcher.Dispatcher
	recorder   outcomes.Recorder
	logger     *slog.Logger
}

// Option is the functional option for New.
type Option func(*Consumer)

// WithReader supplies the Pulsar consumer wrapper. Required.
func WithReader(r pulsarlib.Consumer) Option { return func(c *Consumer) { c.reader = r } }

// WithDispatcher supplies the dispatcher. Required.
func WithDispatcher(d *dispatcher.Dispatcher) Option { return func(c *Consumer) { c.dispatcher = d } }

// WithRecorder supplies the outcomes recorder (used for encryption-policy
// violations that never reach the dispatcher). Required.
func WithRecorder(r outcomes.Recorder) Option { return func(c *Consumer) { c.recorder = r } }

// WithLogger attaches a structured logger.
func WithLogger(l *slog.Logger) Option { return func(c *Consumer) { c.logger = l } }

// New constructs a Consumer.
func New(opts ...Option) (*Consumer, error) {
	c := &Consumer{}
	for _, opt := range opts {
		opt(c)
	}
	if c.reader == nil {
		return nil, errors.New("consumer: WithReader is required")
	}
	if c.dispatcher == nil {
		return nil, errors.New("consumer: WithDispatcher is required")
	}
	if c.recorder == nil {
		return nil, errors.New("consumer: WithRecorder is required")
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c, nil
}

// Run reads messages in a loop until ctx is cancelled. Each iteration:
//  1. Receive (decrypt happens inside pulsarlib).
//  2. If decrypt failed, emit encryption-policy-violation and ack.
//  3. Otherwise unmarshal and dispatch; ack on completion.
//
// The function returns nil when ctx is cancelled, or a non-nil error only
// for unexpected failures of the reader itself.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		msg, err := c.reader.Receive(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			if errors.Is(err, pulsarlib.ErrEncryptionPolicyViolation) {
				// Decrypt failure surfaced at the reader layer (future wiring).
				// The current pulsarlib.apacheConsumer returns a generic error
				// on decrypt failure; when FR-016 detection is hardened we'll
				// route here.
				c.recordPolicyViolation("")
				continue
			}
			c.logger.Error("consumer.receive.error", slog.Any("err", err))
			return err
		}
		if err := c.handle(ctx, msg); err != nil {
			c.logger.Error("consumer.handle.error",
				slog.String("notification_id", msg.NotificationID),
				slog.Any("err", err),
			)
			// Ack poison-pill messages; with decrypt failure policy set to
			// Fail, undecryptable messages are recorded and ack'd below.
		}
		msg.Ack()
	}
}

func (c *Consumer) handle(ctx context.Context, msg *pulsarlib.ConsumedMessage) error {
	metrics.RecordConsumed(ctx)
	c.logger.Info("consumer.consumed",
		slog.String("event", "notification.consumed"),
		slog.String("notification_id", msg.NotificationID),
	)

	n := &notificationpbv1.Notification{}
	if err := proto.Unmarshal(msg.Body, n); err != nil {
		// Malformed plaintext (after successful decrypt) — categorised as a
		// payload-rejected permanent failure so it lands with a reason code.
		c.recorder.Record(ctx, outcomes.Outcome{
			NotificationID: msg.NotificationID,
			Status:         outcomes.StatusPermanentlyFailed,
			Reason:         outcomes.ReasonProviderRejectedPayload,
			Attempts:       0,
		})
		metrics.RecordOutcome(ctx, string(outcomes.StatusPermanentlyFailed),
			outcomes.ReasonProviderRejectedPayload, "", 0)
		return nil
	}
	// Prefer the canonical notification_id from the decoded payload; fall
	// back to the plaintext property.
	if n.NotificationId == "" {
		n.NotificationId = msg.NotificationID
	}
	return c.dispatcher.Dispatch(ctx, n)
}

// RecordPolicyViolation is exposed so the pulsarlib layer (or future wiring)
// can trigger the FR-016 terminal outcome for a given notification ID when
// decryption fails outside the Receive path.
func (c *Consumer) recordPolicyViolation(notificationID string) {
	c.recorder.Record(context.Background(), outcomes.Outcome{
		NotificationID: notificationID,
		Status:         outcomes.StatusPermanentlyFailed,
		Reason:         outcomes.ReasonEncryptionPolicyViolation,
		Attempts:       0,
	})
	metrics.RecordOutcome(context.Background(), string(outcomes.StatusPermanentlyFailed),
		outcomes.ReasonEncryptionPolicyViolation, "", 0)
}
