package pulsarlib_test

import (
	"errors"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
)

func TestApplyDefaults(t *testing.T) {
	o := pulsarlib.Apply(nil)
	if !o.FailOnCryptoError() {
		t.Error("FailOnCryptoError default should be true (FR-016)")
	}
}

func TestApplyOptions(t *testing.T) {
	o := pulsarlib.Apply([]pulsarlib.Option{
		pulsarlib.WithServiceURL("pulsar://localhost:6650"),
		pulsarlib.WithTopic("persistent://public/default/notifications"),
		pulsarlib.WithSubscription("sub"),
		pulsarlib.WithEncryptionKeyNames("v1", "v2"),
		pulsarlib.WithCryptoFailureFail(false),
	})
	if o.ServiceURL() != "pulsar://localhost:6650" {
		t.Errorf("ServiceURL = %q", o.ServiceURL())
	}
	if o.Topic() != "persistent://public/default/notifications" {
		t.Errorf("Topic = %q", o.Topic())
	}
	if o.Subscription() != "sub" {
		t.Errorf("Subscription = %q", o.Subscription())
	}
	names := o.EncryptionKeyNames()
	if len(names) != 2 || names[0] != "v1" || names[1] != "v2" {
		t.Errorf("EncryptionKeyNames = %v", names)
	}
	if o.FailOnCryptoError() {
		t.Error("FailOnCryptoError override to false not honoured")
	}
}

func TestWithEncryptionKeyNamesCopiesSlice(t *testing.T) {
	names := []string{"v1", "v2"}
	o := pulsarlib.Apply([]pulsarlib.Option{pulsarlib.WithEncryptionKeyNames(names...)})
	names[0] = "MUTATED"
	if o.EncryptionKeyNames()[0] != "v1" {
		t.Error("WithEncryptionKeyNames did not defensively copy")
	}
}

func TestPublishErrorUnwrap(t *testing.T) {
	base := errors.New("boom")
	pe := &pulsarlib.PublishError{Transient: true, Err: base}
	if !errors.Is(pe, base) {
		t.Error("errors.Is should unwrap to base")
	}
	if pe.Error() != "boom" {
		t.Errorf("PublishError.Error = %q", pe.Error())
	}
}

func TestNewApachePublisherRequiresOptions(t *testing.T) {
	if _, err := pulsarlib.NewApachePublisher(); err == nil {
		t.Error("expected error from NewApachePublisher with no options")
	}
	if _, err := pulsarlib.NewApachePublisher(pulsarlib.WithServiceURL("x")); err == nil {
		t.Error("expected missing-topic error")
	}
}

func TestNewApacheConsumerRequiresOptions(t *testing.T) {
	if _, err := pulsarlib.NewApacheConsumer(); err == nil {
		t.Error("expected error from NewApacheConsumer with no options")
	}
	if _, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL("x"),
		pulsarlib.WithTopic("t"),
	); err == nil {
		t.Error("expected missing-subscription error")
	}
}
