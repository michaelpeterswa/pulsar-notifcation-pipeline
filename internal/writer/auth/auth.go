// Package auth is the writer's HTTP authentication layer. It defines the
// Authenticator interface (Principle I) and the middleware that enforces it
// before any handler runs. FR-004: unauthenticated requests are rejected with
// an RFC 9457 problem.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
)

// Principal identifies the authenticated caller of an HTTP request. The
// ServiceName propagates into the canonical message's originating_service
// field (FR-003 / data-model.md §1).
type Principal struct {
	ServiceName string
}

// Authenticator validates the credentials carried on an HTTP request and
// returns the identified Principal. Implementations MUST return
// ErrNoCredentials for missing auth and ErrInvalidCredentials for rejected
// auth so the middleware can emit the correct RFC 9457 problem.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (Principal, error)
}

// ErrNoCredentials is returned when no credentials are present on the request.
var ErrNoCredentials = errors.New("auth: no credentials")

// ErrInvalidCredentials is returned when credentials are present but not
// accepted.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// contextKey is a package-private type for context keys to avoid collisions.
type contextKey struct{}

var principalKey contextKey

// FromContext returns the Principal previously placed on ctx by Middleware.
// ok is false if no Principal has been attached.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// RequestIDFunc extracts or constructs the request ID for the current request.
// This is supplied by the router so that auth failures carry the same
// request_id as the eventual problem response. If nil, middleware falls back
// to the X-Request-Id header value.
type RequestIDFunc func(*http.Request) string

// MiddlewareOption is the functional option for Middleware.
type MiddlewareOption func(*middlewareOptions)

type middlewareOptions struct {
	requestID RequestIDFunc
}

// WithRequestIDFunc attaches a request-ID extractor.
func WithRequestIDFunc(fn RequestIDFunc) MiddlewareOption {
	return func(o *middlewareOptions) { o.requestID = fn }
}

// Middleware wraps next in an http.Handler that authenticates via a and
// attaches the resulting Principal to the request context. On failure it
// emits an RFC 9457 problem and does NOT call next.
func Middleware(a Authenticator, next http.Handler, opts ...MiddlewareOption) http.Handler {
	o := middlewareOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Authenticate(r.Context(), r)
		if err != nil {
			rid := ""
			if o.requestID != nil {
				rid = o.requestID(r)
			}
			if rid == "" {
				rid = r.Header.Get("X-Request-Id")
			}
			if errors.Is(err, ErrNoCredentials) {
				problems.Write(w, problems.AuthenticationRequired(rid, r.URL.Path))
				return
			}
			problems.Write(w, problems.AuthenticationFailed(rid, r.URL.Path))
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// BearerToken extracts the token from an Authorization: Bearer <token>
// header. Returns "" if the header is missing or not a bearer credential.
// Implementations of Authenticator that use bearer tokens should use this
// helper so semantics match across implementations.
func BearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
