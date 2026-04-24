//go:build integration || perf

package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/consumer"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/dispatcher"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/outcomes"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/sandbox"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationclient"
)

// acceptingAuth passes any request with a non-empty Authorization header.
type acceptingAuth struct{}

func (acceptingAuth) Authenticate(_ context.Context, r *http.Request) (auth.Principal, error) {
	if auth.BearerToken(r) == "" {
		return auth.Principal{}, auth.ErrNoCredentials
	}
	return auth.Principal{ServiceName: "e2e-test"}, nil
}

func genKeyPair(t *testing.T, dir, name string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".priv.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}), 0o600); err != nil {
		t.Fatal(err)
	}
	pubBytes, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err := os.WriteFile(filepath.Join(dir, name+".pub.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startPulsar(ctx context.Context, t *testing.T) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "apachepulsar/pulsar:3.3.2",
		Cmd:          []string{"bin/pulsar", "standalone", "-nss", "-nfw"},
		ExposedPorts: []string{"6650/tcp", "8080/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("6650/tcp").WithStartupTimeout(180*time.Second),
			wait.ForHTTP("/admin/v2/clusters").WithPort("8080/tcp").WithStartupTimeout(180*time.Second),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatalf("start pulsar: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6650")
	return "pulsar://" + host + ":" + port.Port()
}

type collectingRecorder struct {
	mu      sync.Mutex
	records []outcomes.Outcome
}

func (c *collectingRecorder) Record(_ context.Context, o outcomes.Outcome) {
	c.mu.Lock()
	c.records = append(c.records, o)
	c.mu.Unlock()
}

func (c *collectingRecorder) hasDelivered(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range c.records {
		if o.NotificationID == id && o.Status == outcomes.StatusDelivered {
			return true
		}
	}
	return false
}

// TestPipelineEndToEnd drives the whole pipeline against a real Pulsar
// container: writer → encrypt → Pulsar → decrypt → dispatcher → sandbox
// provider. Satisfies US1's independent test (SC-002, SC-005).
func TestPipelineEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pulsarURL := startPulsar(ctx, t)
	topic := "persistent://public/default/e2e-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	keyDir := t.TempDir()
	genKeyPair(t, keyDir, "v1")
	reader := pulsarlib.NewFileCryptoKeyReader(keyDir)

	// --- writer side. Retry producer creation — the standalone broker's
	// namespace-policy cache lags the HTTP admin endpoint for a few seconds
	// after first boot.
	var publisher pulsarlib.Publisher
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		publisher, err = pulsarlib.NewApachePublisher(
			pulsarlib.WithServiceURL(pulsarURL),
			pulsarlib.WithTopic(topic),
			pulsarlib.WithEncryptionKeyReader(reader),
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
		httpserver.WithRequestIDExtractor(func(_ *http.Request) string { return "rid-e2e" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	rawHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs.SubmitNotification(w, r, httpserver.SubmitNotificationParams{})
	})
	writerSrv := httptest.NewServer(auth.Middleware(acceptingAuth{}, rawHandler))
	defer writerSrv.Close()

	// --- deliverer side
	sb := sandbox.New()
	rec := &collectingRecorder{}
	cons, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL(pulsarURL),
		pulsarlib.WithTopic(topic),
		pulsarlib.WithSubscription("e2e-sub"),
		pulsarlib.WithEncryptionKeyReader(reader),
	)
	if err != nil {
		t.Fatalf("NewApacheConsumer: %v", err)
	}
	defer cons.Close()

	disp, err := dispatcher.New(
		dispatcher.WithProvider("pushover", sb),
		dispatcher.WithRecorder(rec),
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

	// --- submit via the public client library
	client, err := notificationclient.New(writerSrv.URL, notificationclient.WithBearerToken("any"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Submit(ctx, notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "hello", "world"))
	if err != nil {
		t.Fatalf("client.Submit: %v", err)
	}
	if resp.NotificationID() == "" {
		t.Fatal("empty NotificationID from writer")
	}

	// Wait for the sandbox to see it.
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(sb.Delivered()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(sb.Delivered()) == 0 {
		t.Fatalf("no message reached sandbox within deadline")
	}
	delivered := sb.Delivered()[0]
	if delivered.GetNotificationId() != resp.NotificationID() {
		t.Errorf("notification_id mismatch: writer=%s delivered=%s",
			resp.NotificationID(), delivered.GetNotificationId())
	}
	// Wait briefly for the outcome record.
	time.Sleep(500 * time.Millisecond)
	if !rec.hasDelivered(resp.NotificationID()) {
		t.Errorf("no delivered outcome recorded for %s", resp.NotificationID())
	}
}
