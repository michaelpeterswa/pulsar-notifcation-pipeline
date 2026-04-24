// Package pulsarlib is the shared interface-first wrapper that both the writer
// and the reader/deliverer use to talk to Apache Pulsar. Per the project
// constitution's Principle I (Interface-First) and Principle II (Functional
// Options), every exported type is behind an interface and every constructor
// uses the functional-options pattern.
//
// The package also encapsulates Pulsar's native end-to-end encryption
// configuration so that FR-015, FR-016, and FR-017 are implemented in exactly
// one place. Callers do not reach into the Pulsar SDK directly.
package pulsarlib

import (
	"context"
	"errors"
	"log/slog"

	"github.com/apache/pulsar-client-go/pulsar/crypto"
)


// NotificationIDProperty is the Pulsar message property that carries the
// pipeline-assigned notification identifier as plaintext, per FR-015. This
// value is the ONLY field that leaves the encryption envelope.
const NotificationIDProperty = "notification_id"

// PublishInput is the payload-level input to Publisher.Publish. The caller
// supplies the already-serialised ciphertext-bound body plus the plaintext
// notification ID that is exposed as a Pulsar message property.
type PublishInput struct {
	// Body is the protobuf-serialised Notification. Pulsar will encrypt it
	// client-side using the configured public keys before the broker ever
	// sees it (per FR-015).
	Body []byte
	// NotificationID is exposed as a plaintext Pulsar message property for
	// operator correlation (FR-015, SC-005). Nothing else in PublishInput
	// leaves the envelope in plaintext.
	NotificationID string
}

// Publisher is the interface-first wrapper around a Pulsar producer. Only the
// writer binary constructs a Publisher.
type Publisher interface {
	// Publish emits the message to the configured topic under the configured
	// set of active encryption keys. Returns a typed *PublishError so callers
	// can classify transient vs. permanent failures per FR-009 and FR-023.
	Publish(ctx context.Context, in PublishInput) error
	// Close flushes any in-flight messages and releases resources.
	Close() error
}

// ConsumedMessage is one decrypted message delivered to the deliverer by a
// Consumer. The Body is the plaintext protobuf bytes; NotificationID is the
// plaintext Pulsar property (if present). Ack MUST be invoked exactly once
// per consumed message — on successful dispatch OR on a deliberate
// terminal-policy rejection per FR-016.
type ConsumedMessage struct {
	Body           []byte
	NotificationID string
	Ack            func()
	Nack           func()
}

// Consumer is the interface-first wrapper around a Pulsar consumer. Only the
// deliverer binary constructs a Consumer.
type Consumer interface {
	// Receive blocks until the next message arrives or ctx is cancelled. On
	// a decrypt failure the consumer returns ErrEncryptionPolicyViolation so
	// the caller can emit the FR-016 terminal outcome without the message
	// ever reaching the dispatcher.
	Receive(ctx context.Context) (*ConsumedMessage, error)
	// Close releases resources.
	Close() error
}

// PublishError categorises a Publish failure.
type PublishError struct {
	// Transient is true when the caller SHOULD back off and retry; false
	// when the cause is permanent (e.g. message malformed).
	Transient bool
	Err       error
}

func (e *PublishError) Error() string { return e.Err.Error() }
func (e *PublishError) Unwrap() error { return e.Err }

// ErrEncryptionPolicyViolation is returned by Consumer.Receive when a message
// cannot be decrypted under any currently configured private key (FR-016).
// The caller MUST emit a terminal outcome with reason "encryption-policy-
// violation", acknowledge the underlying Pulsar message so it is not
// redelivered, and not dispatch to any provider.
var ErrEncryptionPolicyViolation = errors.New("pulsarlib: encryption policy violation")

// Option is the functional option common to the Publisher and Consumer
// constructors.
type Option func(*options)

type options struct {
	serviceURL          string
	topic               string
	subscription        string // consumer only
	encryptionKeyReader crypto.KeyReader
	encryptionKeyNames  []string // producer only (active public key names)
	failOnCryptoError   bool     // consumer only; defaults to true
	logger              *slog.Logger
}

// WithServiceURL sets the Pulsar broker URL. Required.
func WithServiceURL(u string) Option { return func(o *options) { o.serviceURL = u } }

// WithTopic sets the topic. Required.
func WithTopic(t string) Option { return func(o *options) { o.topic = t } }

// WithSubscription sets the Pulsar subscription name. Consumer-only; required
// when constructing a Consumer.
func WithSubscription(s string) Option { return func(o *options) { o.subscription = s } }

// WithEncryptionKeyReader supplies the crypto.KeyReader used to load
// public keys (producer) or private keys (consumer).
func WithEncryptionKeyReader(r crypto.KeyReader) Option {
	return func(o *options) { o.encryptionKeyReader = r }
}

// WithEncryptionKeyNames names the public keys to encrypt each outgoing
// message to. During a rotation overlap window (FR-017), more than one name
// is supplied. Producer-only.
func WithEncryptionKeyNames(names ...string) Option {
	return func(o *options) { o.encryptionKeyNames = append([]string(nil), names...) }
}

// WithCryptoFailureFail configures the consumer to surface decrypt failures
// as ErrEncryptionPolicyViolation instead of delivering anything to the
// caller. Default; exposed so test suites can opt out deliberately.
func WithCryptoFailureFail(fail bool) Option {
	return func(o *options) { o.failOnCryptoError = fail }
}

// WithLogger attaches a structured logger. Required for observable behaviour;
// optional for unit tests.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

func defaultOptions() options {
	return options{
		failOnCryptoError: true,
	}
}

// Applied configuration accessors — used by both the Apache Pulsar
// implementation and by test fakes.
func (o options) ServiceURL() string                    { return o.serviceURL }
func (o options) Topic() string                          { return o.topic }
func (o options) Subscription() string                   { return o.subscription }
func (o options) EncryptionKeyReader() crypto.KeyReader { return o.encryptionKeyReader }
func (o options) EncryptionKeyNames() []string           { return o.encryptionKeyNames }
func (o options) FailOnCryptoError() bool                { return o.failOnCryptoError }
func (o options) Logger() *slog.Logger                   { return o.logger }

// Apply reduces a slice of options into an options value starting from the
// package defaults. Exposed for the implementation packages and for test
// fakes that need to inspect the composed configuration.
func Apply(opts []Option) options {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
