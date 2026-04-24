// Package config hosts the shared environment-driven configuration used by both
// binaries in the pipeline (writer, deliverer). Per the project constitution's
// Principle V, all configuration flows through environment variables parsed
// via github.com/caarlos0/env/v11; ad-hoc os.Getenv calls are prohibited
// outside this package.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Shared carries configuration values used by both applications. Per-binary
// structs embed this type.
type Shared struct {
	// Logging
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	// Metrics / tracing (shape preserved from the existing scaffold)
	MetricsEnabled    bool    `env:"METRICS_ENABLED" envDefault:"true"`
	MetricsPort       int     `env:"METRICS_PORT" envDefault:"8081"`
	Local             bool    `env:"LOCAL" envDefault:"false"`
	TracingEnabled    bool    `env:"TRACING_ENABLED" envDefault:"false"`
	TracingSampleRate float64 `env:"TRACING_SAMPLERATE" envDefault:"0.01"`
	TracingService    string  `env:"TRACING_SERVICE" envDefault:"pulsar-notifcation-pipeline"`
	TracingVersion    string  `env:"TRACING_VERSION"`

	// Pulsar connection (shared by both apps)
	PulsarServiceURL string `env:"PULSAR_SERVICE_URL" envDefault:"pulsar://localhost:6650"`
	PulsarTopic      string `env:"PULSAR_TOPIC" envDefault:"persistent://public/default/notifications"`

	// Pulsar E2EE keys (shared — writer loads public, deliverer loads private)
	PulsarEncryptionKeyDir string `env:"PULSAR_ENCRYPTION_KEY_DIR,required"`

	// Miscellaneous
	ShutdownGracePeriod time.Duration `env:"SHUTDOWN_GRACE_PERIOD" envDefault:"15s"`
}

// LoadShared parses the shared configuration from the process environment.
func LoadShared() (Shared, error) {
	var s Shared
	if err := env.Parse(&s); err != nil {
		return Shared{}, fmt.Errorf("parse shared config: %w", err)
	}
	return s, nil
}
