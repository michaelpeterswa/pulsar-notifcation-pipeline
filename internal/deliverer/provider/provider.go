// Package provider defines the interface all push-notification backends
// implement (Constitution Principle I, Interface-First). Adding a new
// provider at MVP+N is done by adding a new package under
// internal/deliverer/provider/<name>/ and a new oneof arm on the canonical
// protobuf — no changes required in the writer, substrate, or dispatcher
// wiring beyond the registration point (SC-008).
package provider

import (
	"context"
	"errors"

	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// Result captures what happened at the provider. ProviderResponse is a short
// summary suitable for logging (truncated in outcomes.go to 512 bytes).
type Result struct {
	ProviderResponse string
}

// ErrPermanent is returned by Push when the failure is non-retryable and
// SHOULD terminate the message with a provider-* reason. Callers use
// errors.Is to detect it.
var ErrPermanent = errors.New("provider: permanent failure")

// ErrTransient is returned by Push when the failure is retryable. Callers
// apply backoff and retry up to the configured budget.
var ErrTransient = errors.New("provider: transient failure")

// PermanentReason is an optional extension carried on permanent errors so
// the outcome log emits a precise reason code from data-model.md §5.
type PermanentReason struct {
	Code     string
	Response string
}

// PermanentError wraps a cause with a categorised reason code (from
// data-model.md §5). Callers recover the code via errors.As.
type PermanentError struct {
	Reason   string
	Response string
	Err      error
}

// Error implements error.
func (e *PermanentError) Error() string {
	if e.Err == nil {
		return "provider: permanent failure: " + e.Reason
	}
	return "provider: permanent failure (" + e.Reason + "): " + e.Err.Error()
}

// Unwrap returns ErrPermanent so errors.Is(e, ErrPermanent) is true.
func (e *PermanentError) Unwrap() error { return ErrPermanent }

// TransientError wraps a transient cause.
type TransientError struct {
	Response string
	Err      error
}

// Error implements error.
func (e *TransientError) Error() string {
	if e.Err == nil {
		return "provider: transient failure"
	}
	return "provider: transient failure: " + e.Err.Error()
}

// Unwrap returns ErrTransient so errors.Is(e, ErrTransient) is true.
func (e *TransientError) Unwrap() error { return ErrTransient }

// PushProvider is the Interface-First contract for every push backend.
type PushProvider interface {
	// Name returns the provider identifier (e.g. "pushover", "sandbox"). Used
	// in outcome logs and metric labels.
	Name() string
	// Push dispatches n. Returns (Result, nil) on success; on failure returns
	// nil Result plus either a *PermanentError or a *TransientError.
	Push(ctx context.Context, n *notificationpbv1.Notification) (*Result, error)
}

// NotImplementedError is returned by dispatchers when no provider has been
// registered for the target oneof arm on a Notification.
type NotImplementedError struct{ Target string }

// Error implements error.
func (e *NotImplementedError) Error() string {
	return "provider: no implementation for target " + e.Target
}
