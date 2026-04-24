// Package config is the writer binary's environment-driven configuration.
// It embeds the shared config struct and adds writer-specific variables.
// Per the project constitution's Principle V, configuration is sourced
// exclusively from environment variables via caarlos0/env.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	sharedconfig "github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/config"
)

// Config carries the writer binary's full configuration.
type Config struct {
	sharedconfig.Shared

	HTTPAddr          string        `env:"WRITER_HTTP_ADDR" envDefault:":8080"`
	BearerTokensFile  string        `env:"WRITER_BEARER_TOKENS_FILE,required"`
	IdempotencyWindow time.Duration `env:"WRITER_IDEMPOTENCY_WINDOW" envDefault:"5m"`

	// EncryptionKeyNamesCSV is a comma-separated list of currently active
	// public-key names under PULSAR_ENCRYPTION_KEY_DIR. During key rotation
	// this list may contain more than one name (FR-017 overlap window).
	EncryptionKeyNamesCSV string `env:"PULSAR_ENCRYPTION_KEY_NAMES" envDefault:"v1"`
}

// EncryptionKeyNames returns the parsed list of active public-key names with
// whitespace trimmed and empty entries removed.
func (c Config) EncryptionKeyNames() []string {
	raw := strings.Split(c.EncryptionKeyNamesCSV, ",")
	out := raw[:0]
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Option is the functional option for Config.
type Option func(*Config)

// WithHTTPAddr overrides the HTTP listen address.
func WithHTTPAddr(addr string) Option { return func(c *Config) { c.HTTPAddr = addr } }

// WithBearerTokensFile overrides the bearer-token file path.
func WithBearerTokensFile(p string) Option { return func(c *Config) { c.BearerTokensFile = p } }

// WithIdempotencyWindow overrides the idempotency window.
func WithIdempotencyWindow(d time.Duration) Option {
	return func(c *Config) { c.IdempotencyWindow = d }
}

// WithEncryptionKeyNamesCSV overrides the active public-key names.
func WithEncryptionKeyNamesCSV(csv string) Option {
	return func(c *Config) { c.EncryptionKeyNamesCSV = csv }
}

// Load parses the full writer configuration from the process environment,
// then applies the given options (for test overrides). Returns an error when
// any required variable is missing.
func Load(opts ...Option) (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, fmt.Errorf("parse writer config: %w", err)
	}
	for _, opt := range opts {
		opt(&c)
	}
	if len(c.EncryptionKeyNames()) == 0 {
		return nil, fmt.Errorf("parse writer config: PULSAR_ENCRYPTION_KEY_NAMES must contain at least one name")
	}
	return &c, nil
}
