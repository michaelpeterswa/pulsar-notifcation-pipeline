package config_test

import (
	"testing"
	"time"

	writerconfig "github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/config"
)

func setCommonEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PULSAR_ENCRYPTION_KEY_DIR", "/keys")
	t.Setenv("WRITER_BEARER_TOKENS_FILE", "/tokens")
}

func TestLoadDefaults(t *testing.T) {
	setCommonEnv(t)
	c, err := writerconfig.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.IdempotencyWindow != 5*time.Minute {
		t.Errorf("IdempotencyWindow = %v", c.IdempotencyWindow)
	}
	if names := c.EncryptionKeyNames(); len(names) != 1 || names[0] != "v1" {
		t.Errorf("EncryptionKeyNames = %v", names)
	}
}

func TestLoadRequiresTokensFile(t *testing.T) {
	t.Setenv("PULSAR_ENCRYPTION_KEY_DIR", "/keys")
	if _, err := writerconfig.Load(); err == nil {
		t.Fatal("expected missing WRITER_BEARER_TOKENS_FILE to error")
	}
}

func TestKeyNamesParsedCSV(t *testing.T) {
	setCommonEnv(t)
	t.Setenv("PULSAR_ENCRYPTION_KEY_NAMES", " v1 , v2 ,,v3")
	c, err := writerconfig.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	names := c.EncryptionKeyNames()
	if len(names) != 3 || names[0] != "v1" || names[1] != "v2" || names[2] != "v3" {
		t.Errorf("EncryptionKeyNames = %v", names)
	}
}

func TestLoadAppliesOptions(t *testing.T) {
	setCommonEnv(t)
	c, err := writerconfig.Load(
		writerconfig.WithHTTPAddr(":9999"),
		writerconfig.WithIdempotencyWindow(30*time.Second),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.IdempotencyWindow != 30*time.Second {
		t.Errorf("IdempotencyWindow = %v", c.IdempotencyWindow)
	}
}

func TestLoadRejectsEmptyKeyNames(t *testing.T) {
	setCommonEnv(t)
	t.Setenv("PULSAR_ENCRYPTION_KEY_NAMES", " , ")
	if _, err := writerconfig.Load(); err == nil {
		t.Fatal("expected empty key names to error")
	}
}
