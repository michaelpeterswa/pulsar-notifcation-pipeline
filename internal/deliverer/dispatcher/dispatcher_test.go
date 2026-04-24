package dispatcher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

type memRecorder struct {
	mu      sync.Mutex
	records []outcomes.Outcome
}

func (m *memRecorder) Record(_ context.Context, o outcomes.Outcome) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, o)
}

type stubProvider struct {
	name string
	res  *provider.Result
	err  error
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Push(_ context.Context, _ *notificationpbv1.Notification) (*provider.Result, error) {
	return s.res, s.err
}

func pushoverNotif() *notificationpbv1.Notification {
	return &notificationpbv1.Notification{
		NotificationId: "nid-x",
		Content:        &notificationpbv1.Content{Title: "t", Body: "b"},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{UserOrGroupKey: "k"},
		},
	}
}

func TestDispatchDelivered(t *testing.T) {
	rec := &memRecorder{}
	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", &stubProvider{name: "pushover", res: &provider.Result{ProviderResponse: "ok"}}),
		dispatcher.WithRecorder(rec),
	)
	if err := d.Dispatch(context.Background(), pushoverNotif()); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(rec.records) != 1 {
		t.Fatalf("records = %d", len(rec.records))
	}
	o := rec.records[0]
	if o.Status != outcomes.StatusDelivered {
		t.Errorf("status = %q", o.Status)
	}
	if o.NotificationID != "nid-x" {
		t.Errorf("notification_id = %q", o.NotificationID)
	}
	if o.Attempts != 1 {
		t.Errorf("attempts = %d", o.Attempts)
	}
}

func TestDispatchPermanentError(t *testing.T) {
	rec := &memRecorder{}
	pe := &provider.PermanentError{Reason: outcomes.ReasonProviderRejectedRecipient, Err: errors.New("bad key")}
	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", &stubProvider{err: pe}),
		dispatcher.WithRecorder(rec),
	)
	_ = d.Dispatch(context.Background(), pushoverNotif())
	if len(rec.records) != 1 {
		t.Fatalf("records = %d", len(rec.records))
	}
	if rec.records[0].Status != outcomes.StatusPermanentlyFailed {
		t.Errorf("status = %q", rec.records[0].Status)
	}
	if rec.records[0].Reason != outcomes.ReasonProviderRejectedRecipient {
		t.Errorf("reason = %q", rec.records[0].Reason)
	}
}

func TestDispatchTransientBecomesTerminalInUS1(t *testing.T) {
	rec := &memRecorder{}
	te := &provider.TransientError{Err: errors.New("5xx")}
	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", &stubProvider{err: te}),
		dispatcher.WithRecorder(rec),
	)
	_ = d.Dispatch(context.Background(), pushoverNotif())
	if rec.records[0].Status != outcomes.StatusPermanentlyFailed {
		t.Errorf("status = %q", rec.records[0].Status)
	}
	if rec.records[0].Reason != outcomes.ReasonRetryBudgetExhausted {
		t.Errorf("reason = %q (US1 has no retry loop yet)", rec.records[0].Reason)
	}
}

func TestDispatchExpired(t *testing.T) {
	rec := &memRecorder{}
	// Clock advances so ExpiresAt is in the past.
	fixed := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", &stubProvider{}),
		dispatcher.WithRecorder(rec),
		dispatcher.WithClock(func() time.Time { return fixed }),
	)
	n := pushoverNotif()
	n.ExpiresAt = timestamppb.New(fixed.Add(-1 * time.Hour))
	_ = d.Dispatch(context.Background(), n)
	if rec.records[0].Status != outcomes.StatusExpired {
		t.Errorf("status = %q", rec.records[0].Status)
	}
}

func TestDispatchUnknownTarget(t *testing.T) {
	rec := &memRecorder{}
	d, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", &stubProvider{}),
		dispatcher.WithRecorder(rec),
	)
	n := pushoverNotif()
	n.Target = nil // unknown arm
	_ = d.Dispatch(context.Background(), n)
	if rec.records[0].Reason != outcomes.ReasonProviderRejectedPayload {
		t.Errorf("reason = %q", rec.records[0].Reason)
	}
}

func TestNewRequiresProviderAndRecorder(t *testing.T) {
	if _, err := dispatcher.New(dispatcher.WithRecorder(&memRecorder{})); err == nil {
		t.Error("expected error without providers")
	}
	if _, err := dispatcher.New(dispatcher.WithProvider("pushover", &stubProvider{})); err == nil {
		t.Error("expected error without recorder")
	}
}
