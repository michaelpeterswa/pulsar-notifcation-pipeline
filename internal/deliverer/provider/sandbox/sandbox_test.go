package sandbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

func notif(id string) *notificationpbv1.Notification {
	return &notificationpbv1.Notification{NotificationId: id}
}

func TestDeliveredRecordsSuccesses(t *testing.T) {
	p := sandbox.New()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := p.Push(context.Background(), notif(id)); err != nil {
			t.Fatalf("Push %s: %v", id, err)
		}
	}
	if got := len(p.Delivered()); got != 3 {
		t.Errorf("Delivered count = %d", got)
	}
}

func TestScriptedErrorsConsumedInOrder(t *testing.T) {
	transient := &provider.TransientError{Err: errors.New("try again")}
	permanent := &provider.PermanentError{Reason: "provider-rejected-payload", Err: errors.New("nope")}
	p := sandbox.New(sandbox.WithScriptedErrors(transient, nil, permanent))

	// attempt 1 → transient
	_, err := p.Push(context.Background(), notif("1"))
	if !errors.Is(err, provider.ErrTransient) {
		t.Errorf("attempt 1 err = %v", err)
	}
	// attempt 2 → success
	if _, err := p.Push(context.Background(), notif("2")); err != nil {
		t.Errorf("attempt 2 err = %v", err)
	}
	// attempt 3 → permanent
	_, err = p.Push(context.Background(), notif("3"))
	if !errors.Is(err, provider.ErrPermanent) {
		t.Errorf("attempt 3 err = %v", err)
	}
	// attempt 4 → success (script exhausted)
	if _, err := p.Push(context.Background(), notif("4")); err != nil {
		t.Errorf("attempt 4 err = %v", err)
	}
	delivered := p.Delivered()
	if len(delivered) != 2 {
		t.Fatalf("delivered = %d, want 2", len(delivered))
	}
	if delivered[0].NotificationId != "2" || delivered[1].NotificationId != "4" {
		t.Errorf("delivered IDs = %v %v", delivered[0].NotificationId, delivered[1].NotificationId)
	}
}

func TestPushRejectsNil(t *testing.T) {
	p := sandbox.New()
	_, err := p.Push(context.Background(), nil)
	if !errors.Is(err, provider.ErrPermanent) {
		t.Errorf("nil notification err = %v", err)
	}
}

func TestName(t *testing.T) {
	if sandbox.New().Name() != "sandbox" {
		t.Error("sandbox Name()")
	}
}
