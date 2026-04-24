package config_test

import (
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/config"
)

func TestLoadSharedDefaults(t *testing.T) {
	t.Setenv("PULSAR_ENCRYPTION_KEY_DIR", "/run/secrets/pulsar-keys")
	// Leave all other variables unset to exercise defaults.

	s, err := config.LoadShared()
	if err != nil {
		t.Fatalf("LoadShared: %v", err)
	}
	if s.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want %q", s.LogLevel, "info")
	}
	if s.PulsarServiceURL != "pulsar://localhost:6650" {
		t.Errorf("PulsarServiceURL default = %q", s.PulsarServiceURL)
	}
	if s.PulsarEncryptionKeyDir != "/run/secrets/pulsar-keys" {
		t.Errorf("PulsarEncryptionKeyDir = %q", s.PulsarEncryptionKeyDir)
	}
	if s.ShutdownGracePeriod.Seconds() != 15 {
		t.Errorf("ShutdownGracePeriod default = %v", s.ShutdownGracePeriod)
	}
}

func TestLoadSharedRequiresKeyDir(t *testing.T) {
	// No PULSAR_ENCRYPTION_KEY_DIR set — must fail.
	if _, err := config.LoadShared(); err == nil {
		t.Fatal("LoadShared without PULSAR_ENCRYPTION_KEY_DIR: expected error, got nil")
	}
}

func TestLoadSharedOverrides(t *testing.T) {
	t.Setenv("PULSAR_ENCRYPTION_KEY_DIR", "/keys")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("PULSAR_TOPIC", "persistent://tenant/ns/topic")
	t.Setenv("TRACING_ENABLED", "true")
	t.Setenv("SHUTDOWN_GRACE_PERIOD", "42s")

	s, err := config.LoadShared()
	if err != nil {
		t.Fatalf("LoadShared: %v", err)
	}
	if s.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", s.LogLevel)
	}
	if s.PulsarTopic != "persistent://tenant/ns/topic" {
		t.Errorf("PulsarTopic = %q", s.PulsarTopic)
	}
	if !s.TracingEnabled {
		t.Errorf("TracingEnabled = false")
	}
	if s.ShutdownGracePeriod.Seconds() != 42 {
		t.Errorf("ShutdownGracePeriod = %v", s.ShutdownGracePeriod)
	}
}
