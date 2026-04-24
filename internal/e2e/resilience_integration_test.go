//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/pushover"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/retry"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationclient"
)

// TestResilienceRetryRecovers spins up a real Pulsar, a flaky "Pushover"
// httptest that returns 503 for the first N requests then 200, and verifies
// the deliverer retries until the real push lands — satisfying US2's
// independent test.
func TestResilienceRetryRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pulsarURL := startPulsar(ctx, t)
	topic := "persistent://public/default/resil-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	keyDir := t.TempDir()
	genKeyPair(t, keyDir, "v1")
	kr := pulsarlib.NewFileCryptoKeyReader(keyDir)

	// Flaky Pushover httptest server: first 2 requests respond 503; then 200.
	var requests atomic.Int32
	pushoverSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n <= 2 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"status":0}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer pushoverSrv.Close()

	// --- writer side with producer retry for Pulsar namespace cache.
	var publisher pulsarlib.Publisher
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		publisher, err = pulsarlib.NewApachePublisher(
			pulsarlib.WithServiceURL(pulsarURL),
			pulsarlib.WithTopic(topic),
			pulsarlib.WithEncryptionKeyReader(kr),
			pulsarlib.WithEncryptionKeyNames("v1"),
		)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatalf("NewApachePublisher: %v", err)
	}
	defer publisher.Close()

	ing, err := ingest.NewIngester(ingest.WithPublisher(publisher))
	if err != nil {
		t.Fatal(err)
	}
	hs, err := httpserver.NewHandlers(
		httpserver.WithIngester(ing),
		httpserver.WithRequestIDExtractor(func(_ *http.Request) string { return "rid-resil" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	rawHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs.SubmitNotification(w, r, httpserver.SubmitNotificationParams{})
	})
	writerSrv := httptest.NewServer(auth.Middleware(acceptingAuth{}, rawHandler))
	defer writerSrv.Close()

	// --- deliverer side using the real Pushover provider against our stub.
	push, err := pushover.New(
		pushover.WithAppToken("fake"),
		pushover.WithBaseURL(pushoverSrv.URL),
	)
	if err != nil {
		t.Fatal(err)
	}
	rec := &collectingRecorder{}
	cons, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL(pulsarURL),
		pulsarlib.WithTopic(topic),
		pulsarlib.WithSubscription("resil-sub"),
		pulsarlib.WithEncryptionKeyReader(kr),
	)
	if err != nil {
		t.Fatalf("NewApacheConsumer: %v", err)
	}
	defer cons.Close()

	disp, err := dispatcher.New(
		dispatcher.WithProvider("pushover", push),
		dispatcher.WithRecorder(rec),
		dispatcher.WithRetryPolicy(retry.NewPolicy(
			retry.WithMaxAttempts(5),
			retry.WithInitialBackoff(50*time.Millisecond),
			retry.WithMaxBackoff(500*time.Millisecond),
			retry.WithRNG(func() float64 { return 0.1 }),
		)),
	)
	if err != nil {
		t.Fatal(err)
	}
	delCtx, delCancel := context.WithCancel(ctx)
	defer delCancel()
	consumerLoop, err := consumer.New(
		consumer.WithReader(cons),
		consumer.WithDispatcher(disp),
		consumer.WithRecorder(rec),
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = consumerLoop.Run(delCtx) }()

	// --- submit one notification
	client, err := notificationclient.New(writerSrv.URL, notificationclient.WithBearerToken("any"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Submit(ctx, notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "hello", "world"))
	if err != nil {
		t.Fatalf("client.Submit: %v", err)
	}

	// Wait for the delivered outcome (should land on 3rd Pushover attempt).
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if rec.hasDelivered(resp.NotificationID()) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !rec.hasDelivered(resp.NotificationID()) {
		t.Fatalf("no delivered outcome for %s after retries; requests=%d records=%+v",
			resp.NotificationID(), requests.Load(), rec.snapshot())
	}
	if requests.Load() < 3 {
		t.Errorf("expected >= 3 Pushover requests (first 2 are 503), got %d", requests.Load())
	}

	// Confirm the delivered record's Attempts > 1 (retries happened).
	var delivered outcomes.Outcome
	for _, o := range rec.snapshot() {
		if o.NotificationID == resp.NotificationID() && o.Status == outcomes.StatusDelivered {
			delivered = o
			break
		}
	}
	if delivered.Attempts < 2 {
		t.Errorf("delivered outcome Attempts = %d (want >=2 after transient failures)", delivered.Attempts)
	}
}

// snapshot returns a defensive copy of the recorded outcomes.
func (c *collectingRecorder) snapshot() []outcomes.Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]outcomes.Outcome, len(c.records))
	copy(out, c.records)
	return out
}

// keep imports used across Go tag combinations.
var _ = errors.Is
var _ = sync.Mutex{}
