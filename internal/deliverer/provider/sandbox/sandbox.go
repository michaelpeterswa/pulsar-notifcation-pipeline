// Package sandbox is an in-memory PushProvider used for tests and local
// demos. It records every Push call and can be configured to return
// transient or permanent failures on a schedule. Exported so integration
// tests in internal/e2e/ can assert delivery end-to-end without a real
// push service.
package sandbox

import (
	"context"
	"errors"
	"sync"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// Provider is the in-memory push target.
type Provider struct {
	mu        sync.Mutex
	delivered []*notificationpbv1.Notification

	// scripted is a queue of errors to return before the normal success
	// path resumes. nil at index i means "succeed on attempt i".
	scripted []error
	pos      int
}

// Option is the functional option for New.
type Option func(*Provider)

// WithScriptedErrors queues up a sequence of responses. Each Push consumes
// the next script entry; a nil entry yields a success, non-nil is returned.
// After the script is exhausted, Pushes succeed.
func WithScriptedErrors(seq ...error) Option {
	return func(p *Provider) { p.scripted = seq }
}

// New constructs a sandbox provider.
func New(opts ...Option) *Provider {
	p := &Provider{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements provider.PushProvider.
func (p *Provider) Name() string { return "sandbox" }

// Push implements provider.PushProvider. Thread-safe.
func (p *Provider) Push(_ context.Context, n *notificationpbv1.Notification) (*provider.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pos < len(p.scripted) {
		err := p.scripted[p.pos]
		p.pos++
		if err != nil {
			return nil, err
		}
	}
	if n == nil {
		return nil, &provider.PermanentError{Reason: "provider-rejected-payload", Err: errors.New("nil notification")}
	}
	p.delivered = append(p.delivered, n)
	return &provider.Result{ProviderResponse: "sandbox:accepted"}, nil
}

// Delivered returns a snapshot of notifications dispatched successfully.
func (p *Provider) Delivered() []*notificationpbv1.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*notificationpbv1.Notification, len(p.delivered))
	copy(out, p.delivered)
	return out
}

// Attempts returns the number of Push calls that have been made (including
// failed ones driven by scripted errors).
func (p *Provider) Attempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos + len(p.delivered)
}
