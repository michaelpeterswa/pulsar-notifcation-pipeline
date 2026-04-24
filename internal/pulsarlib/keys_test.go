package pulsarlib_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
)

func writeKeyFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFileCryptoKeyReaderPublicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	writeKeyFile(t, filepath.Join(dir, "v1.pub.pem"), "--PUB v1--")
	writeKeyFile(t, filepath.Join(dir, "v1.priv.pem"), "--PRIV v1--")

	r := pulsarlib.NewFileCryptoKeyReader(dir)

	pub, err := r.PublicKey("v1", nil)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if string(pub.Key()) != "--PUB v1--" {
		t.Errorf("public key bytes = %q", pub.Key())
	}
	if pub.Name() != "v1" {
		t.Errorf("public key name = %q", pub.Name())
	}

	priv, err := r.PrivateKey("v1", nil)
	if err != nil {
		t.Fatalf("PrivateKey: %v", err)
	}
	if string(priv.Key()) != "--PRIV v1--" {
		t.Errorf("private key bytes = %q", priv.Key())
	}
}

func TestFileCryptoKeyReaderMissing(t *testing.T) {
	dir := t.TempDir()
	r := pulsarlib.NewFileCryptoKeyReader(dir)

	if _, err := r.PublicKey("missing", nil); err == nil {
		t.Error("expected error for missing public key")
	} else if !strings.Contains(err.Error(), "missing") {
		t.Errorf("err does not name the key: %v", err)
	}
	if _, err := r.PrivateKey("gone", nil); err == nil {
		t.Error("expected error for missing private key")
	}
}

func TestLoadKeySet(t *testing.T) {
	dir := t.TempDir()
	writeKeyFile(t, filepath.Join(dir, "v1.pub.pem"), "v1pub")
	writeKeyFile(t, filepath.Join(dir, "v2.pub.pem"), "v2pub")

	ks, err := pulsarlib.LoadKeySet(dir, []string{"v1", "v2"})
	if err != nil {
		t.Fatalf("LoadKeySet: %v", err)
	}
	if len(ks.ActiveNames) != 2 {
		t.Errorf("ActiveNames = %v", ks.ActiveNames)
	}

	if _, err := pulsarlib.LoadKeySet(dir, nil); err == nil {
		t.Error("expected error on empty activeNames")
	}
	if _, err := pulsarlib.LoadKeySet(dir, []string{"v3"}); err == nil {
		t.Error("expected error on missing active key")
	}
}
