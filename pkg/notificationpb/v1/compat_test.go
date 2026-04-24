package notificationpbv1_test

import (
	"testing"

	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
	prev "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1/prev"

	"google.golang.org/protobuf/proto"
)

// TestWireCompatForward marshals with the current generation and unmarshals
// with the frozen previous generation. This guards FR-013 / SC-007 against
// the "old reader, new writer" direction.
func TestWireCompatForward(t *testing.T) {
	in := sampleNotification()
	buf, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("current Marshal: %v", err)
	}
	out := &prev.Notification{}
	if err := proto.Unmarshal(buf, out); err != nil {
		t.Fatalf("prev Unmarshal rejected current-generation bytes: %v", err)
	}
	if out.GetNotificationId() != in.GetNotificationId() {
		t.Fatalf("notification_id not preserved across generations: want %q got %q",
			in.GetNotificationId(), out.GetNotificationId())
	}
}

// TestWireCompatBackward marshals with the frozen previous generation and
// unmarshals with the current generation. This guards the "new reader, old
// writer" direction.
func TestWireCompatBackward(t *testing.T) {
	in := samplePrevNotification()
	buf, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("prev Marshal: %v", err)
	}
	out := &notificationpbv1.Notification{}
	if err := proto.Unmarshal(buf, out); err != nil {
		t.Fatalf("current Unmarshal rejected prev-generation bytes: %v", err)
	}
	if out.GetNotificationId() != in.GetNotificationId() {
		t.Fatalf("notification_id not preserved across generations: want %q got %q",
			in.GetNotificationId(), out.GetNotificationId())
	}
}

func samplePrevNotification() *prev.Notification {
	return &prev.Notification{
		NotificationId:     "01933dcc-2b8f-7d44-9aab-52c6f1c0a5d7",
		OriginatingService: "test-service",
		Content: &prev.Content{
			Title: "hello",
			Body:  "world",
		},
		Target: &prev.Notification_Pushover{
			Pushover: &prev.PushoverTarget{
				UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
			},
		},
	}
}
