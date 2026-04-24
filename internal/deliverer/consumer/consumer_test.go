package consumer_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

type memRecorder struct {
	mu      sync.Mutex
	records []outcomes.Outcome
}

func (m *memRecorder) Record(_ context.Context, o outcomes.Outcome) {
	m.mu.Lock()
	m.records = append(m.records, o)
	m.mu.Unlock()
}

type scriptedReader struct {
	mu     sync.Mutex
	queue  []any // *pulsarlib.ConsumedMessage or error
	idx    int
	acks   int
	closed bool
}

func (s *scriptedReader) Receive(ctx context.Context) (*pulsarlib.ConsumedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx >= len(s.queue) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	item := s.queue[s.idx]
	s.idx++
	switch v := item.(type) {
	case error:
		return nil, v
	case *pulsarlib.ConsumedMessage:
		v.Ack = func() { s.mu.Lock(); s.acks++; s.mu.Unlock() }
		v.Nack = func() {}
		return v, nil
	}
	return nil, errors.New("unreachable")
}

func (s *scriptedReader) Close() error { s.closed = true; return nil }

func validProto(id string) []byte {
	n := &notificationpbv1.Notification{
		NotificationId: id,
		Content:        &notificationpbv1.Content{Title: "t", Body: "b"},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG"},
		},
	}
	buf, _ := proto.Marshal(n)
	return buf
}

func newConsumerUnderTest(t *testing.T, reader pulsarlib.Consumer, sb provider.PushProvider) (*consumer.Consumer, *memRecorder) {
	t.Helper()
	rec := &memRecorder{}
	d, err := dispatcher.New(
		dispatcher.WithProvider("pushover", sb),
		dispatcher.WithRecorder(rec),
	)
	if err != nil {
		t.Fatalf("dispatcher.New: %v", err)
	}
	c, err := consumer.New(
		consumer.WithReader(reader),
		consumer.WithDispatcher(d),
		consumer.WithRecorder(rec),
	)
	if err != nil {
		t.Fatalf("consumer.New: %v", err)
	}
	return c, rec
}

func TestConsumerDispatchesSuccessfulMessage(t *testing.T) {
	reader := &scriptedReader{queue: []any{
		&pulsarlib.ConsumedMessage{Body: validProto("nid-1"), NotificationID: "nid-1"},
	}}
	sb := sandbox.New()
	c, rec := newConsumerUnderTest(t, reader, sb)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(sb.Delivered()) != 1 {
		t.Errorf("delivered count = %d", len(sb.Delivered()))
	}
	if len(rec.records) != 1 || rec.records[0].Status != outcomes.StatusDelivered {
		t.Errorf("outcomes = %+v", rec.records)
	}
	if reader.acks != 1 {
		t.Errorf("acks = %d", reader.acks)
	}
}

func TestConsumerMalformedBody(t *testing.T) {
	reader := &scriptedReader{queue: []any{
		&pulsarlib.ConsumedMessage{Body: []byte("not-protobuf"), NotificationID: "nid-malformed"},
	}}
	sb := sandbox.New()
	c, rec := newConsumerUnderTest(t, reader, sb)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.records) != 1 {
		t.Fatalf("records = %d", len(rec.records))
	}
	if rec.records[0].Reason != outcomes.ReasonProviderRejectedPayload {
		t.Errorf("reason = %q", rec.records[0].Reason)
	}
	if len(sb.Delivered()) != 0 {
		t.Error("sandbox should not have been called on malformed body")
	}
	if reader.acks != 1 {
		t.Error("malformed body must still be acked")
	}
}

func TestConsumerEncryptionPolicyViolation(t *testing.T) {
	reader := &scriptedReader{queue: []any{
		pulsarlib.ErrEncryptionPolicyViolation,
	}}
	sb := sandbox.New()
	c, rec := newConsumerUnderTest(t, reader, sb)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = c.Run(ctx)

	found := false
	for _, o := range rec.records {
		if o.Reason == outcomes.ReasonEncryptionPolicyViolation {
			found = true
		}
	}
	if !found {
		t.Errorf("expected encryption-policy-violation outcome; got %+v", rec.records)
	}
	if len(sb.Delivered()) != 0 {
		t.Error("sandbox must not be called on policy violation")
	}
}

func TestNewRequiresAllDeps(t *testing.T) {
	if _, err := consumer.New(); err == nil {
		t.Error("expected error without deps")
	}
}
