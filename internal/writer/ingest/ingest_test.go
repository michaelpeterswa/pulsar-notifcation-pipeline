package ingest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

type fakePublisher struct {
	last pulsarlib.PublishInput
	err  error
}

func (f *fakePublisher) Publish(_ context.Context, in pulsarlib.PublishInput) error {
	f.last = in
	return f.err
}
func (f *fakePublisher) Close() error { return nil }

func validNotification() *notificationpbv1.Notification {
	return &notificationpbv1.Notification{
		Content: &notificationpbv1.Content{
			Title: "hello",
			Body:  "world",
		},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{
				UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
			},
		},
	}
}

func TestAcceptHappyPath(t *testing.T) {
	pub := &fakePublisher{}
	fixed := time.Date(2026, 4, 23, 18, 0, 0, 123_456_789, time.UTC)

	ing, err := ingest.NewIngester(
		ingest.WithPublisher(pub),
		ingest.WithClock(func() time.Time { return fixed }),
		ingest.WithIDFunc(func() (string, error) { return "fixed-id", nil }),
	)
	if err != nil {
		t.Fatalf("NewIngester: %v", err)
	}

	res, err := ing.Accept(context.Background(), validNotification(), "svc-x")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if res.NotificationID != "fixed-id" {
		t.Errorf("NotificationID = %q", res.NotificationID)
	}
	if !res.AcceptedAt.Equal(fixed.Truncate(time.Millisecond)) {
		t.Errorf("AcceptedAt = %v (want %v)", res.AcceptedAt, fixed.Truncate(time.Millisecond))
	}
	if pub.last.NotificationID != "fixed-id" {
		t.Errorf("publish NotificationID = %q", pub.last.NotificationID)
	}
	if len(pub.last.Body) == 0 {
		t.Error("publish body empty")
	}
}

func TestAcceptRejectsNil(t *testing.T) {
	ing, _ := ingest.NewIngester(ingest.WithPublisher(&fakePublisher{}))
	_, err := ing.Accept(context.Background(), nil, "svc")
	var verr *ingest.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestAcceptPropagatesPublishError(t *testing.T) {
	pubErr := &pulsarlib.PublishError{Transient: true, Err: errors.New("broker down")}
	pub := &fakePublisher{err: pubErr}
	ing, _ := ingest.NewIngester(ingest.WithPublisher(pub))

	_, err := ing.Accept(context.Background(), validNotification(), "svc")
	var pe *pulsarlib.PublishError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *pulsarlib.PublishError, got %T: %v", err, err)
	}
	if !pe.Transient {
		t.Error("expected transient=true")
	}
}

func TestValidateSurfaceCatalogue(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*notificationpbv1.Notification)
		expect string // expected field error "field:code"
	}{
		{"missing-title", func(n *notificationpbv1.Notification) { n.Content.Title = "" }, "content.title:required"},
		{"title-too-long", func(n *notificationpbv1.Notification) {
			n.Content.Title = strings.Repeat("x", 251)
		}, "content.title:too-long"},
		{"title-whitespace-only", func(n *notificationpbv1.Notification) { n.Content.Title = "   " }, "content.title:whitespace-only"},
		{"missing-body", func(n *notificationpbv1.Notification) { n.Content.Body = "" }, "content.body:required"},
		{"body-too-long", func(n *notificationpbv1.Notification) {
			n.Content.Body = strings.Repeat("y", 1025)
		}, "content.body:too-long"},
		{"priority-out-of-range", func(n *notificationpbv1.Notification) { n.Content.Priority = 5 }, "content.priority:out-of-range"},
		{"data-too-many", func(n *notificationpbv1.Notification) {
			m := make(map[string]string, 33)
			for i := 0; i < 33; i++ {
				m["k"+string(rune('a'+i%26))+string(rune('0'+i/26))] = "v"
			}
			n.Content.Data = m
		}, "content.data:too-many-entries"},
		{"data-key-invalid", func(n *notificationpbv1.Notification) {
			n.Content.Data = map[string]string{"0bad": "v"}
		}, "content.data:key-invalid"},
		{"data-value-too-long", func(n *notificationpbv1.Notification) {
			n.Content.Data = map[string]string{"k": strings.Repeat("v", 513)}
		}, "content.data:value-too-long"},
		{"target-missing", func(n *notificationpbv1.Notification) { n.Target = nil }, "target:missing"},
		{"pushover-key-required", func(n *notificationpbv1.Notification) {
			n.Target.(*notificationpbv1.Notification_Pushover).Pushover.UserOrGroupKey = ""
		}, "target.pushover.user_or_group_key:required"},
		{"pushover-key-malformed", func(n *notificationpbv1.Notification) {
			n.Target.(*notificationpbv1.Notification_Pushover).Pushover.UserOrGroupKey = "shortkey"
		}, "target.pushover.user_or_group_key:malformed"},
		{"device-malformed", func(n *notificationpbv1.Notification) {
			n.Target.(*notificationpbv1.Notification_Pushover).Pushover.DeviceName = "not valid!"
		}, "target.pushover.device_name:malformed"},
		{"url-malformed", func(n *notificationpbv1.Notification) {
			n.Target.(*notificationpbv1.Notification_Pushover).Pushover.Url = "not-a-url"
		}, "target.pushover.url:malformed"},
		{"url-title-requires-url", func(n *notificationpbv1.Notification) {
			n.Target.(*notificationpbv1.Notification_Pushover).Pushover.UrlTitle = "Open"
		}, "target.pushover.url_title:requires-url"},
		{"expires-before-created", func(n *notificationpbv1.Notification) {
			n.CreatedAt = timestamppb.New(time.Now())
			n.ExpiresAt = timestamppb.New(time.Now().Add(-time.Hour))
		}, "expires_at:before-created-at"},
		{"idempotency-too-long", func(n *notificationpbv1.Notification) {
			n.IdempotencyKey = strings.Repeat("k", 129)
		}, "idempotency_key:too-long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validNotification()
			tc.mutate(n)
			verr := ingest.Validate(n)
			if verr == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
			if !hasFieldError(verr.Fields, tc.expect) {
				t.Errorf("missing %s in %v", tc.expect, verr.Fields)
			}
		})
	}
}

func hasFieldError(errs []problems.FieldError, want string) bool {
	for _, e := range errs {
		if e.Field+":"+e.Code == want {
			return true
		}
	}
	return false
}

func TestValidateHappyPath(t *testing.T) {
	if verr := ingest.Validate(validNotification()); verr != nil {
		t.Fatalf("validNotification rejected: %v", verr.Fields)
	}
}

func TestNewIngesterRequiresPublisher(t *testing.T) {
	if _, err := ingest.NewIngester(); err == nil {
		t.Error("expected error without WithPublisher")
	}
}
