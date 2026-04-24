// Package outcomes records the terminal status of every notification
// processed by the deliverer (data-model.md §4, §5; FR-011).
package outcomes

import (
	"context"
	"log/slog"
	"time"
)

// Status is the terminal outcome. Never "in progress" — outcomes are only
// recorded when processing reaches a final state.
type Status string

// StatusDelivered, StatusPermanentlyFailed, and StatusExpired are the terminal outcome statuses.
const (
	StatusDelivered         Status = "delivered"
	StatusPermanentlyFailed Status = "permanently-failed"
	StatusExpired           Status = "expired"
)

// Reason codes from data-model.md §5.
const (
	ReasonEncryptionPolicyViolation = "encryption-policy-violation"
	ReasonProviderRejectedRecipient = "provider-rejected-recipient"
	ReasonProviderRejectedPayload   = "provider-rejected-payload"
	ReasonProviderAuthFailed        = "provider-auth-failed"
	ReasonRetryBudgetExhausted      = "retry-budget-exhausted"
	ReasonExpired                   = "expired"
)

// Outcome is one terminal record.
type Outcome struct {
	NotificationID   string
	Status           Status
	Reason           string
	ProviderResponse string
	Attempts         int
	FirstAttempt     time.Time
	LastAttempt      time.Time
}

// Recorder is the Interface-First abstraction over how outcomes are persisted
// or emitted. The default implementation writes a structured slog event per
// outcome; a future feature could add a metrics-writing recorder or a
// persistent store without touching dispatcher/consumer code.
type Recorder interface {
	Record(ctx context.Context, o Outcome)
}

// SlogRecorder emits outcomes as structured slog events at Info level
// (warn-level when not "delivered" to surface on dashboards by default).
type SlogRecorder struct {
	logger *slog.Logger
}

// Option is the functional option for NewSlogRecorder.
type Option func(*SlogRecorder)

// WithLogger supplies the underlying slog.Logger. If unset, slog.Default is
// used.
func WithLogger(l *slog.Logger) Option { return func(s *SlogRecorder) { s.logger = l } }

// NewSlogRecorder constructs a SlogRecorder.
func NewSlogRecorder(opts ...Option) *SlogRecorder {
	s := &SlogRecorder{}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// Record implements Recorder.
func (s *SlogRecorder) Record(_ context.Context, o Outcome) {
	attrs := []any{
		slog.String("event", "notification.outcome"),
		slog.String("notification_id", o.NotificationID),
		slog.String("status", string(o.Status)),
		slog.Int("attempts", o.Attempts),
	}
	if o.Reason != "" {
		attrs = append(attrs, slog.String("reason", o.Reason))
	}
	if o.ProviderResponse != "" {
		attrs = append(attrs, slog.String("provider_response", truncate(o.ProviderResponse, 512)))
	}
	if !o.FirstAttempt.IsZero() {
		attrs = append(attrs, slog.Time("first_attempt", o.FirstAttempt))
	}
	if !o.LastAttempt.IsZero() {
		attrs = append(attrs, slog.Time("last_attempt", o.LastAttempt))
	}
	if o.Status == StatusDelivered {
		s.logger.Info("notification outcome", attrs...)
	} else {
		s.logger.Warn("notification outcome", attrs...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
