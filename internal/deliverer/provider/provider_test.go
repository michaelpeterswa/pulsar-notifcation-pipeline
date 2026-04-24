package provider_test

import (
	"errors"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
)

func TestPermanentErrorIs(t *testing.T) {
	cause := errors.New("rootcause")
	pe := &provider.PermanentError{Reason: "provider-rejected-recipient", Err: cause}
	if !errors.Is(pe, provider.ErrPermanent) {
		t.Error("errors.Is(*PermanentError, ErrPermanent) should be true")
	}
	if errors.Is(pe, provider.ErrTransient) {
		t.Error("permanent error must not match ErrTransient")
	}
}

func TestTransientErrorIs(t *testing.T) {
	te := &provider.TransientError{Err: errors.New("5xx")}
	if !errors.Is(te, provider.ErrTransient) {
		t.Error("errors.Is(*TransientError, ErrTransient) should be true")
	}
}

func TestPermanentErrorAs(t *testing.T) {
	pe := &provider.PermanentError{Reason: "provider-rejected-payload"}
	var target *provider.PermanentError
	if !errors.As(pe, &target) {
		t.Fatal("errors.As failed")
	}
	if target.Reason != "provider-rejected-payload" {
		t.Errorf("Reason = %q", target.Reason)
	}
}

func TestNotImplementedError(t *testing.T) {
	e := &provider.NotImplementedError{Target: "apns"}
	if e.Error() == "" {
		t.Fatal("empty error message")
	}
}
