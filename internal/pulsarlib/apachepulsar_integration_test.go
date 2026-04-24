//go:build integration

package pulsarlib_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// genRSAKeyPair produces a PEM-encoded RSA 2048 key pair. Pulsar Go
// client's default message crypto parses private keys via
// x509.ParsePKCS1PrivateKey, so the private key must be RSA PKCS1-encoded.
// The public key is PKIX-encoded (handles both RSA and ECDSA on the Pulsar
// side, but we use RSA here for symmetry with the private key).
func genRSAKeyPair(t *testing.T, dir, name string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	// private — PKCS1 format
	if err := os.WriteFile(filepath.Join(dir, name+".priv.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}), 0o600); err != nil {
		t.Fatalf("write priv: %v", err)
	}
	// public — PKIX format
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".pub.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0o644); err != nil {
		t.Fatalf("write pub: %v", err)
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
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start pulsar: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "6650")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return "pulsar://" + host + ":" + port.Port()
}

func TestEndToEndEncryptDecrypt(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	url := startPulsar(ctx, t)
	keyDir := t.TempDir()
	genRSAKeyPair(t, keyDir, "v1")

	reader := pulsarlib.NewFileCryptoKeyReader(keyDir)
	topic := "persistent://public/default/it-" + t.Name()

	// The standalone broker's namespace-policy cache lags the HTTP admin
	// endpoint by a second or two after first start; retry producer creation
	// until the namespace is ready.
	var pub pulsarlib.Publisher
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		pub, err = pulsarlib.NewApachePublisher(
			pulsarlib.WithServiceURL(url),
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
	defer pub.Close()

	cons, err := pulsarlib.NewApacheConsumer(
		pulsarlib.WithServiceURL(url),
		pulsarlib.WithTopic(topic),
		pulsarlib.WithSubscription("it-sub"),
		pulsarlib.WithEncryptionKeyReader(reader),
	)
	if err != nil {
		t.Fatalf("NewApacheConsumer: %v", err)
	}
	defer cons.Close()

	payload := []byte("hello, ciphertext")
	if err := pub.Publish(ctx, pulsarlib.PublishInput{
		Body:           payload,
		NotificationID: "nid-123",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
	defer rcancel()
	msg, err := cons.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Body) != string(payload) {
		t.Errorf("body = %q, want %q", msg.Body, payload)
	}
	if msg.NotificationID != "nid-123" {
		t.Errorf("notification_id property = %q", msg.NotificationID)
	}
	msg.Ack()
}
