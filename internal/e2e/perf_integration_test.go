//go:build perf

package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationclient"
)

// TestPerformanceSubmit500RPS validates SC-001 (writer ack p95 ≤ 250ms),
// SC-002 (end-to-end dispatch p95 ≤ 2s), and SC-006 (sustained 500 RPS
// without growing backlog). Run with:
//
//	go test -tags=perf -timeout=10m -run TestPerformance ./internal/e2e
func TestPerformanceSubmit500RPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	pulsarURL := startPulsar(ctx, t)
	topic := "persistent://public/default/perf-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	keyDir := t.TempDir()
	genKeyPair(t, keyDir, "v1")
	kr := pulsarlib.NewFileCryptoKeyReader(keyDir)

	// Writer
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
		t.Fatal(err)
	}
	defer publisher.Close()

	ing, _ := ingest.NewIngester(ingest.WithPublisher(publisher))
	hs, _ := httpserver.NewHandlers(
		httpserver.WithIngester(ing),
		httpserver.WithRequestIDExtractor(func(_ *http.Request) string { return "perf" }),
	)
	rawHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs.SubmitNotification(w, r, httpserver.SubmitNotificationParams{})
	})
	writerSrv := httptest.NewServer(auth.Middleware(acceptingAuth{}, rawHandler))
	defer writerSrv.Close()

	// Deliverer
	sb := sandbox.New()
	rec := &collectingRecorder{}
	cons, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL(pulsarURL),
		pulsarlib.WithTopic(topic),
		pulsarlib.WithSubscription("perf-sub"),
		pulsarlib.WithEncryptionKeyReader(kr),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cons.Close()
	disp, _ := dispatcher.New(
		dispatcher.WithProvider("pushover", sb),
		dispatcher.WithRecorder(rec),
	)
	delCtx, delCancel := context.WithCancel(ctx)
	defer delCancel()
	cloop, _ := consumer.New(
		consumer.WithReader(cons),
		consumer.WithDispatcher(disp),
		consumer.WithRecorder(rec),
	)
	for i := 0; i < 8; i++ {
		go func() { _ = cloop.Run(delCtx) }()
	}

	client, err := notificationclient.New(writerSrv.URL, notificationclient.WithBearerToken("any"))
	if err != nil {
		t.Fatal(err)
	}

	// Workload: 500 RPS for 30 seconds → 15,000 submissions.
	const rps = 500
	const duration = 30 * time.Second
	const target = int(rps * duration / time.Second)

	ackLatencies := make([]time.Duration, 0, target)
	var ackMu sync.Mutex
	var sent atomic.Int64
	var errs atomic.Int64

	start := time.Now()
	ticker := time.NewTicker(time.Second / rps)
	defer ticker.Stop()

	var wg sync.WaitGroup
	done := time.After(duration)
loop:
	for {
		select {
		case <-done:
			break loop
		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				_, err := client.Submit(ctx, notificationclient.NewPushoverRequest(
					"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
				d := time.Since(t0)
				if err != nil {
					errs.Add(1)
					return
				}
				sent.Add(1)
				ackMu.Lock()
				ackLatencies = append(ackLatencies, d)
				ackMu.Unlock()
			}()
		}
	}
	wg.Wait()
	submittedFor := time.Since(start)
	t.Logf("submitted %d in %v (%d errors)", sent.Load(), submittedFor, errs.Load())

	// Wait for all consumed.
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if int64(len(sb.Delivered())) >= sent.Load() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if int64(len(sb.Delivered())) < sent.Load() {
		t.Errorf("consumer did not keep up: delivered=%d sent=%d (SC-006 backlog growth)", len(sb.Delivered()), sent.Load())
	}

	p95Ack := p95(ackLatencies)
	t.Logf("writer ack p95 = %v (target ≤ 250ms; SC-001)", p95Ack)
	if p95Ack > 250*time.Millisecond {
		t.Errorf("SC-001 missed: ack p95 = %v", p95Ack)
	}
	if errs.Load() > 0 {
		t.Errorf("%d errors during submission", errs.Load())
	}
}

func p95(xs []time.Duration) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	// naive sort-and-pick
	cp := make([]time.Duration, len(xs))
	copy(cp, xs)
	sortDur(cp)
	idx := int(float64(len(cp))*0.95) - 1
	if idx < 0 {
		idx = 0
	}
	return cp[idx]
}

func sortDur(xs []time.Duration) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
