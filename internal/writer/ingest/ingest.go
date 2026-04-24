// Package ingest is the writer's validate-assign-publish pipeline. It takes
// a canonical Notification supplied by the caller, validates it per
// data-model.md §1-§3, assigns the server-controlled fields (notification_id,
// created_at, originating_service), and publishes the ciphertext to Pulsar
// via pulsarlib.Publisher. FR-003, FR-005, FR-010, FR-015.
package ingest

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/metrics"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

// ValidationError is returned by Accept when the caller's Notification fails
// canonical-schema validation. Each FieldError is emitted in the
// `errors` extension member of the RFC 9457 response.
type ValidationError struct {
	Fields []problems.FieldError
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("ingest: validation failed (%d field(s))", len(e.Fields))
}

// Ingester validates and publishes Notifications. Constructed via
// functional options per Principle II.
type Ingester struct {
	publisher pulsarlib.Publisher
	clock     func() time.Time
	idFn      func() (string, error)
}

// Option is the functional option for NewIngester.
type Option func(*Ingester)

// WithPublisher supplies the Pulsar publisher. Required.
func WithPublisher(p pulsarlib.Publisher) Option { return func(i *Ingester) { i.publisher = p } }

// WithClock overrides the time source. Test-only.
func WithClock(fn func() time.Time) Option { return func(i *Ingester) { i.clock = fn } }

// WithIDFunc overrides the notification-ID generator. Test-only.
func WithIDFunc(fn func() (string, error)) Option { return func(i *Ingester) { i.idFn = fn } }

// NewIngester constructs an Ingester. Required option: WithPublisher.
func NewIngester(opts ...Option) (*Ingester, error) {
	i := &Ingester{
		clock: time.Now,
		idFn:  newUUIDv7,
	}
	for _, opt := range opts {
		opt(i)
	}
	if i.publisher == nil {
		return nil, fmt.Errorf("ingest: WithPublisher is required")
	}
	return i, nil
}

func newUUIDv7() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// AcceptResult is the result of a successful Accept. The caller uses it to
// build the writer's {data, meta} success envelope.
type AcceptResult struct {
	NotificationID string
	AcceptedAt     time.Time
}

// Accept validates n, assigns server-controlled fields using originatingService,
// and publishes the resulting protobuf to Pulsar. Returns either a
// *ValidationError (caller fault) or an error wrapping *pulsarlib.PublishError
// (substrate fault).
func (i *Ingester) Accept(ctx context.Context, n *notificationpbv1.Notification, originatingService string) (*AcceptResult, error) {
	if n == nil {
		return nil, &ValidationError{Fields: []problems.FieldError{
			{Field: "notification", Code: "required", Message: "request body is empty"},
		}}
	}
	if verr := Validate(n); verr != nil {
		return nil, verr
	}

	now := i.clock().UTC().Truncate(time.Millisecond)
	id, err := i.idFn()
	if err != nil {
		return nil, fmt.Errorf("ingest: generate notification_id: %w", err)
	}

	n.NotificationId = id
	n.CreatedAt = timestamppb.New(now)
	n.OriginatingService = originatingService

	body, err := proto.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("ingest: marshal notification: %w", err)
	}
	pubStart := time.Now()
	err = i.publisher.Publish(ctx, pulsarlib.PublishInput{
		Body:           body,
		NotificationID: id,
	})
	metrics.RecordPublishDuration(ctx, time.Since(pubStart).Seconds())
	if err != nil {
		return nil, err
	}
	return &AcceptResult{NotificationID: id, AcceptedAt: now}, nil
}

// --- validation -------------------------------------------------------------

var (
	// 30 alphanumeric characters (Pushover user/group key format).
	pushoverKeyRE = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	// Device name allowed characters.
	deviceRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// Allowed characters for Content.data map keys.
	dataKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate runs the canonical-schema validation rules from data-model.md. The
// returned ValidationError's Fields slice contains one entry per violation.
// Returns nil when n is valid.
func Validate(n *notificationpbv1.Notification) *ValidationError {
	var errs []problems.FieldError

	validateContent(n.GetContent(), &errs)
	validateTarget(n.GetTarget(), &errs)
	validateTimestamps(n, &errs)
	validateIdempotency(n.GetIdempotencyKey(), &errs)

	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Fields: errs}
}

func validateContent(c *notificationpbv1.Content, errs *[]problems.FieldError) {
	if c == nil {
		*errs = append(*errs,
			problems.FieldError{Field: "content.title", Code: "required"},
			problems.FieldError{Field: "content.body", Code: "required"},
		)
		return
	}
	switch {
	case c.GetTitle() == "":
		*errs = append(*errs, problems.FieldError{Field: "content.title", Code: "required"})
	case strings.TrimSpace(c.GetTitle()) == "":
		*errs = append(*errs, problems.FieldError{Field: "content.title", Code: "whitespace-only"})
	case utf8.RuneCountInString(c.GetTitle()) > 250:
		*errs = append(*errs, problems.FieldError{Field: "content.title", Code: "too-long"})
	}
	switch {
	case c.GetBody() == "":
		*errs = append(*errs, problems.FieldError{Field: "content.body", Code: "required"})
	case utf8.RuneCountInString(c.GetBody()) > 1024:
		*errs = append(*errs, problems.FieldError{Field: "content.body", Code: "too-long"})
	}
	if p := c.GetPriority(); p < -2 || p > 2 {
		*errs = append(*errs, problems.FieldError{
			Field:   "content.priority",
			Code:    "out-of-range",
			Message: "must be in [-2, 2]",
		})
	}
	if len(c.GetData()) > 32 {
		*errs = append(*errs, problems.FieldError{Field: "content.data", Code: "too-many-entries"})
	}
	for k, v := range c.GetData() {
		if len(k) > 64 || !dataKeyRE.MatchString(k) {
			*errs = append(*errs, problems.FieldError{Field: "content.data", Code: "key-invalid", Message: k})
		}
		if len(v) > 512 {
			*errs = append(*errs, problems.FieldError{Field: "content.data", Code: "value-too-long", Message: k})
		}
	}
}

func validateTarget(t any, errs *[]problems.FieldError) {
	switch arm := t.(type) {
	case nil:
		*errs = append(*errs, problems.FieldError{Field: "target", Code: "missing"})
	case *notificationpbv1.Notification_Pushover:
		validatePushover(arm.Pushover, errs)
	default:
		*errs = append(*errs, problems.FieldError{Field: "target", Code: "unknown-arm"})
	}
}

func validatePushover(t *notificationpbv1.PushoverTarget, errs *[]problems.FieldError) {
	if t == nil {
		*errs = append(*errs,
			problems.FieldError{Field: "target.pushover.user_or_group_key", Code: "required"},
		)
		return
	}
	switch {
	case t.GetUserOrGroupKey() == "":
		*errs = append(*errs, problems.FieldError{Field: "target.pushover.user_or_group_key", Code: "required"})
	case !pushoverKeyRE.MatchString(t.GetUserOrGroupKey()):
		*errs = append(*errs, problems.FieldError{Field: "target.pushover.user_or_group_key", Code: "malformed"})
	}
	if d := t.GetDeviceName(); d != "" {
		if len(d) > 25 {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.device_name", Code: "too-long"})
		}
		if !deviceRE.MatchString(d) {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.device_name", Code: "malformed"})
		}
	}
	if u := t.GetUrl(); u != "" {
		if len(u) > 512 {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.url", Code: "too-long"})
		} else if parsed, err := url.Parse(u); err != nil || !parsed.IsAbs() {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.url", Code: "malformed"})
		}
	}
	if ut := t.GetUrlTitle(); ut != "" {
		if len(ut) > 100 {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.url_title", Code: "too-long"})
		}
		if t.GetUrl() == "" {
			*errs = append(*errs, problems.FieldError{Field: "target.pushover.url_title", Code: "requires-url"})
		}
	}
}

func validateTimestamps(n *notificationpbv1.Notification, errs *[]problems.FieldError) {
	if n.GetExpiresAt() != nil && n.GetCreatedAt() != nil {
		if n.GetExpiresAt().AsTime().Before(n.GetCreatedAt().AsTime()) {
			*errs = append(*errs, problems.FieldError{Field: "expires_at", Code: "before-created-at"})
		}
	}
}

func validateIdempotency(k string, errs *[]problems.FieldError) {
	if len(k) > 128 {
		*errs = append(*errs, problems.FieldError{Field: "idempotency_key", Code: "too-long"})
	}
}
