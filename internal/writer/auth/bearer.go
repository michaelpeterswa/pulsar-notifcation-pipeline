package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Bearer is an Authenticator backed by a static mapping of bearer tokens to
// service names. The mapping is loaded from a file at construction time and
// is immutable thereafter; operators rotate tokens by restarting the writer
// with an updated file (see research.md §7).
type Bearer struct {
	tokens map[string]Principal
}

// BearerOption is the functional option for NewBearer.
type BearerOption func(*bearerOptions)

type bearerOptions struct {
	tokensPath string
}

// WithTokensFile sets the path to the tokens file. Required.
func WithTokensFile(p string) BearerOption {
	return func(o *bearerOptions) { o.tokensPath = p }
}

// NewBearer loads the bearer-token file from the path supplied via
// WithTokensFile and constructs a Bearer authenticator.
//
// File format: one entry per line, either
//
//	<token>
//	<token> <service-name>
//
// Lines with a leading '#' and empty lines are ignored. A missing service
// name defaults to "unknown".
func NewBearer(opts ...BearerOption) (*Bearer, error) {
	o := bearerOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.tokensPath == "" {
		return nil, errors.New("auth: WithTokensFile is required")
	}
	f, err := os.Open(o.tokensPath)
	if err != nil {
		return nil, fmt.Errorf("auth: open tokens file: %w", err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]Principal{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		token := parts[0]
		service := "unknown"
		if len(parts) >= 2 {
			service = parts[1]
		}
		out[token] = Principal{ServiceName: service}
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("auth: scan tokens file: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("auth: tokens file is empty")
	}
	return &Bearer{tokens: out}, nil
}

// Authenticate validates the Bearer credential on r against the loaded token
// set.
func (b *Bearer) Authenticate(_ context.Context, r *http.Request) (Principal, error) {
	token := BearerToken(r)
	if token == "" {
		return Principal{}, ErrNoCredentials
	}
	p, ok := b.tokens[token]
	if !ok {
		return Principal{}, ErrInvalidCredentials
	}
	return p, nil
}
