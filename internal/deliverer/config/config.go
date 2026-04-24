// Package config is the deliverer binary's environment-driven configuration
// (Constitution Principle V).
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	sharedconfig "github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/config"
)

// ProviderKind selects which PushProvider the deliverer instantiates.
type ProviderKind string

// ProviderPushover and ProviderSandbox are the supported push provider values.
const (
	ProviderPushover ProviderKind = "pushover"
	ProviderSandbox  ProviderKind = "sandbox"
)

// Config is the deliverer binary's full configuration.
type Config struct {
	sharedconfig.Shared

	PulsarSubscription  string        `env:"PULSAR_SUBSCRIPTION" envDefault:"notifications-deliverer"`
	Concurrency         int           `env:"DELIVERER_CONCURRENCY" envDefault:"4"`
	RetryMaxAttempts    int           `env:"DELIVERER_RETRY_MAX_ATTEMPTS" envDefault:"5"`
	RetryInitialBackoff time.Duration `env:"DELIVERER_RETRY_INITIAL_BACKOFF" envDefault:"500ms"`
	RetryMaxBackoff     time.Duration `env:"DELIVERER_RETRY_MAX_BACKOFF" envDefault:"30s"`
	Provider            ProviderKind  `env:"PROVIDER" envDefault:"pushover"`
	PushoverAppToken    string        `env:"PUSHOVER_APP_TOKEN"`
	PushoverBaseURL     string        `env:"PUSHOVER_BASE_URL" envDefault:"https://api.pushover.net"`
}

// Option is the functional option for Load.
type Option func(*Config)

// WithConcurrency overrides concurrency.
func WithConcurrency(n int) Option { return func(c *Config) { c.Concurrency = n } }

// WithProvider overrides the push provider selection.
func WithProvider(k ProviderKind) Option { return func(c *Config) { c.Provider = k } }

// Load parses the deliverer configuration from the environment and applies
// the supplied overrides.
func Load(opts ...Option) (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse deliverer config: %w", err)
	}
	for _, opt := range opts {
		opt(&c)
	}
	switch c.Provider {
	case ProviderPushover:
		if c.PushoverAppToken == "" {
			return nil, fmt.Errorf("parse deliverer config: PUSHOVER_APP_TOKEN is required when PROVIDER=pushover")
		}
	case ProviderSandbox:
		// no extra requirements
	default:
		return nil, fmt.Errorf("parse deliverer config: unknown PROVIDER %q", c.Provider)
	}
	if c.Concurrency <= 0 {
		return nil, fmt.Errorf("parse deliverer config: DELIVERER_CONCURRENCY must be > 0")
	}
	if c.RetryMaxAttempts <= 0 {
		return nil, fmt.Errorf("parse deliverer config: DELIVERER_RETRY_MAX_ATTEMPTS must be > 0")
	}
	return &c, nil
}
