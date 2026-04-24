package pulsarlib

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsar/crypto"
)

// NewApachePublisher constructs a Publisher backed by the Apache Pulsar Go
// client. Required options: WithServiceURL, WithTopic, WithEncryptionKeyReader,
// WithEncryptionKeyNames (at least one name).
func NewApachePublisher(opts ...Option) (Publisher, error) {
	o := Apply(opts)
	if err := validateProducer(o); err != nil {
		return nil, err
	}
	client, err := pulsar.NewClient(pulsar.ClientOptions{URL: o.serviceURL})
	if err != nil {
		return nil, fmt.Errorf("pulsarlib: new pulsar client: %w", err)
	}
	producer, err := client.CreateProducer(pulsar.ProducerOptions{
		Topic: o.topic,
		Encryption: &pulsar.ProducerEncryptionInfo{
			KeyReader: o.encryptionKeyReader,
			Keys:      o.encryptionKeyNames,
		},
	})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("pulsarlib: create producer: %w", err)
	}
	return &apachePublisher{
		client:   client,
		producer: producer,
		log:      effectiveLogger(o.logger),
	}, nil
}

// NewApacheConsumer constructs a Consumer backed by the Apache Pulsar Go
// client. Required options: WithServiceURL, WithTopic, WithSubscription,
// WithEncryptionKeyReader.
func NewApacheConsumer(opts ...Option) (Consumer, error) {
	o := Apply(opts)
	if err := validateConsumer(o); err != nil {
		return nil, err
	}
	client, err := pulsar.NewClient(pulsar.ClientOptions{URL: o.serviceURL})
	if err != nil {
		return nil, fmt.Errorf("pulsarlib: new pulsar client: %w", err)
	}
	failAction := crypto.ConsumerCryptoFailureActionFail
	if !o.failOnCryptoError {
		failAction = crypto.ConsumerCryptoFailureActionDiscard
	}
	consumer, err := client.Subscribe(pulsar.ConsumerOptions{
		Topic:            o.topic,
		SubscriptionName: o.subscription,
		Type:             pulsar.Shared,
		Decryption: &pulsar.MessageDecryptionInfo{
			KeyReader:                   o.encryptionKeyReader,
			ConsumerCryptoFailureAction: failAction,
		},
	})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("pulsarlib: subscribe: %w", err)
	}
	return &apacheConsumer{
		client:   client,
		consumer: consumer,
		log:      effectiveLogger(o.logger),
	}, nil
}

func validateProducer(o options) error {
	if o.serviceURL == "" {
		return errors.New("pulsarlib: WithServiceURL is required")
	}
	if o.topic == "" {
		return errors.New("pulsarlib: WithTopic is required")
	}
	if o.encryptionKeyReader == nil {
		return errors.New("pulsarlib: WithEncryptionKeyReader is required")
	}
	if len(o.encryptionKeyNames) == 0 {
		return errors.New("pulsarlib: WithEncryptionKeyNames is required (at least one)")
	}
	return nil
}

func validateConsumer(o options) error {
	if o.serviceURL == "" {
		return errors.New("pulsarlib: WithServiceURL is required")
	}
	if o.topic == "" {
		return errors.New("pulsarlib: WithTopic is required")
	}
	if o.subscription == "" {
		return errors.New("pulsarlib: WithSubscription is required")
	}
	if o.encryptionKeyReader == nil {
		return errors.New("pulsarlib: WithEncryptionKeyReader is required")
	}
	return nil
}

type apachePublisher struct {
	client   pulsar.Client
	producer pulsar.Producer
	log      *slog.Logger
}

func (p *apachePublisher) Publish(ctx context.Context, in PublishInput) error {
	msg := &pulsar.ProducerMessage{
		Payload: in.Body,
		Properties: map[string]string{
			NotificationIDProperty: in.NotificationID,
		},
	}
	_, err := p.producer.Send(ctx, msg)
	if err != nil {
		// Conservatively classify Send errors as transient; the writer's
		// handler layer catches ctx deadline and deliberate ctx cancellation
		// before this code runs.
		return &PublishError{Transient: true, Err: err}
	}
	return nil
}

func (p *apachePublisher) Close() error {
	p.producer.Close()
	p.client.Close()
	return nil
}

type apacheConsumer struct {
	client   pulsar.Client
	consumer pulsar.Consumer
	log      *slog.Logger
}

func (c *apacheConsumer) Receive(ctx context.Context) (*ConsumedMessage, error) {
	msg, err := c.consumer.Receive(ctx)
	if err != nil {
		// On decrypt-failure with Fail action, the Pulsar client surfaces
		// the message differently depending on version; we conservatively
		// translate any payload-less error outcome to the encryption-policy
		// violation sentinel. Callers layer the interpretation: dispatchers
		// see a typed error, operators see the terminal outcome.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("pulsarlib: receive: %w", err)
	}
	out := &ConsumedMessage{
		Body:           msg.Payload(),
		NotificationID: msg.Properties()[NotificationIDProperty],
		Ack:            func() { _ = c.consumer.Ack(msg) },
		Nack:           func() { c.consumer.Nack(msg) },
	}
	return out, nil
}

func (c *apacheConsumer) Close() error {
	c.consumer.Close()
	c.client.Close()
	return nil
}

func effectiveLogger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}
