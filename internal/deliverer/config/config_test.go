package config_test

import (
	"testing"
	"time"

	delivererconfig "github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/config"
)

func set(t *testing.T) {
	t.Helper()
	t.Setenv("PULSAR_ENCRYPTION_KEY_DIR", "/keys")
}

func TestLoadDefaultsPushoverRequiresToken(t *testing.T) {
	set(t)
	if _, err := delivererconfig.Load(); err == nil {
		t.Fatal("expected missing PUSHOVER_APP_TOKEN error")
	}
}

func TestLoadSandbox(t *testing.T) {
	set(t)
	t.Setenv("PROVIDER", "sandbox")
	c, err := delivererconfig.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Provider != delivererconfig.ProviderSandbox {
		t.Errorf("Provider = %q", c.Provider)
	}
	if c.Concurrency != 4 {
		t.Errorf("Concurrency default = %d", c.Concurrency)
	}
	if c.RetryInitialBackoff != 500*time.Millisecond {
		t.Errorf("RetryInitialBackoff default = %v", c.RetryInitialBackoff)
	}
}

func TestLoadPushover(t *testing.T) {
	set(t)
	t.Setenv("PUSHOVER_APP_TOKEN", "app-token-xyz")
	c, err := delivererconfig.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PushoverAppToken != "app-token-xyz" {
		t.Errorf("PushoverAppToken = %q", c.PushoverAppToken)
	}
	if c.PushoverBaseURL != "https://api.pushover.net" {
		t.Errorf("PushoverBaseURL default = %q", c.PushoverBaseURL)
	}
}

func TestLoadUnknownProvider(t *testing.T) {
	set(t)
	t.Setenv("PROVIDER", "wavelink")
	if _, err := delivererconfig.Load(); err == nil {
		t.Fatal("expected unknown-provider error")
	}
}

func TestLoadInvalidConcurrency(t *testing.T) {
	set(t)
	t.Setenv("PROVIDER", "sandbox")
	t.Setenv("DELIVERER_CONCURRENCY", "0")
	if _, err := delivererconfig.Load(); err == nil {
		t.Fatal("expected invalid-concurrency error")
	}
}

func TestLoadAppliesOptions(t *testing.T) {
	set(t)
	t.Setenv("PROVIDER", "sandbox")
	c, err := delivererconfig.Load(
		delivererconfig.WithConcurrency(16),
		delivererconfig.WithProvider(delivererconfig.ProviderSandbox),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Concurrency != 16 {
		t.Errorf("Concurrency = %d", c.Concurrency)
	}
}
