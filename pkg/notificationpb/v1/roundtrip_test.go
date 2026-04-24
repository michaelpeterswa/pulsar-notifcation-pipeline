package notificationpbv1_test

import (
	"testing"
	"time"

	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/go-cmp/cmp"
)

func sampleNotification() *notificationpbv1.Notification {
	created := time.Date(2026, 4, 23, 18, 0, 0, 123_000_000, time.UTC)
	expires := created.Add(5 * time.Minute)
	return &notificationpbv1.Notification{
		NotificationId:     "01933dcc-2b8f-7d44-9aab-52c6f1c0a5d7",
		CreatedAt:          timestamppb.New(created),
		ExpiresAt:          timestamppb.New(expires),
		OriginatingService: "test-service",
		IdempotencyKey:     "abc-123",
		Content: &notificationpbv1.Content{
			Title:    "hello",
			Body:     "world",
			Priority: 1,
			Data:     map[string]string{"source": "test", "ref": "42"},
		},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{
				UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
				DeviceName:     "iphone",
				Sound:          "pushover",
				Url:            "https://example.com/x",
				UrlTitle:       "Open",
			},
		},
	}
}

func TestRoundtripProtobuf(t *testing.T) {
	in := sampleNotification()
	buf, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := &notificationpbv1.Notification{}
	if err := proto.Unmarshal(buf, got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if diff := cmp.Diff(in, got, protocmp.Transform()); diff != "" {
		t.Fatalf("proto roundtrip mismatch (-want +got):\n%s", diff)
	}
}

func TestRoundtripJSON(t *testing.T) {
	in := sampleNotification()
	buf, err := protojson.Marshal(in)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	got := &notificationpbv1.Notification{}
	if err := protojson.Unmarshal(buf, got); err != nil {
		t.Fatalf("protojson.Unmarshal: %v", err)
	}
	if diff := cmp.Diff(in, got, protocmp.Transform()); diff != "" {
		t.Fatalf("protojson roundtrip mismatch (-want +got):\n%s", diff)
	}
}

func TestOneofUnsetRoundtrips(t *testing.T) {
	n := sampleNotification()
	n.Target = nil
	buf, err := proto.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := &notificationpbv1.Notification{}
	if err := proto.Unmarshal(buf, got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GetTarget() != nil {
		t.Fatalf("expected nil target, got %T", got.GetTarget())
	}
}
