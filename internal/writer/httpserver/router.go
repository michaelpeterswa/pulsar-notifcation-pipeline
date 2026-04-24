package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
)

// requestIDKey is the unexported context key for per-request correlation IDs.
type requestIDKey struct{}

// RequestIDFromContext returns the server-generated request ID attached by
// the router's middleware. Returns the empty string if none is present.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// ServerOption is the functional option for NewServer.
type ServerOption func(*Server)

// Server is the writer's HTTP server.
type Server struct {
	handler       *Handlers
	authenticator auth.Authenticator
	logger        *slog.Logger

	http *http.Server
}

// WithHandler supplies the Handlers implementation. Required.
func WithHandler(h *Handlers) ServerOption { return func(s *Server) { s.handler = h } }

// WithAuthenticator supplies the Authenticator. Required.
func WithAuthenticator(a auth.Authenticator) ServerOption {
	return func(s *Server) { s.authenticator = a }
}

// WithLogger supplies the structured logger.
func WithLogger(l *slog.Logger) ServerOption { return func(s *Server) { s.logger = l } }

// WithAddress sets the listen address.
func WithAddress(addr string) ServerOption {
	return func(s *Server) {
		if s.http == nil {
			s.http = &http.Server{}
		}
		s.http.Addr = addr
	}
}

// NewServer wires the HTTP server's middleware stack and routes. Required
// options: WithHandler, WithAuthenticator, WithAddress.
func NewServer(opts ...ServerOption) (*Server, error) {
	s := &Server{}
	for _, opt := range opts {
		opt(s)
	}
	if s.handler == nil {
		return nil, errors.New("httpserver: WithHandler is required")
	}
	if s.authenticator == nil {
		return nil, errors.New("httpserver: WithAuthenticator is required")
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.http == nil || s.http.Addr == "" {
		return nil, errors.New("httpserver: WithAddress is required")
	}

	r := mux.NewRouter()
	r.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	// The generated ServerInterfaceWrapper routes /notifications to
	// s.handler.SubmitNotification.
	wrapped := &ServerInterfaceWrapper{
		Handler:          s.handler,
		ErrorHandlerFunc: nil,
	}
	r.Methods(http.MethodPost).Path("/notifications").HandlerFunc(wrapped.SubmitNotification)

	// Protected subtree: everything POST /notifications and below needs auth.
	// The auth middleware wraps the gorilla mux router for this path.
	authed := auth.Middleware(s.authenticator, r,
		auth.WithRequestIDFunc(func(r *http.Request) string { return RequestIDFromContext(r.Context()) }),
	)

	// Outer chain: recover → request-id → logger → auth → route
	chain := s.recoverMiddleware(
		s.requestIDMiddleware(
			s.accessLogMiddleware(
				conditionalAuth(s.authenticator, r, authed),
			),
		),
	)
	s.http.Handler = chain

	return s, nil
}

// Addr returns the listen address.
func (s *Server) Addr() string { return s.http.Addr }

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error { return s.http.ListenAndServe() }

// Shutdown gracefully terminates the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// Handler returns the root http.Handler (useful for httptest).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// conditionalAuth routes /healthz to the unauthenticated router, everything
// else to the authenticated handler chain.
func conditionalAuth(_ auth.Authenticator, raw *mux.Router, authed http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			raw.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// requestIDMiddleware generates a ULID-ish ID (actually UUIDv7 for convenience)
// and attaches it to both the context and the response header.
func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			if u, err := uuid.NewV7(); err == nil {
				rid = "req_" + u.String()
			} else {
				rid = "req_unknown"
			}
		}
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, rid)))
	})
}

// accessLogMiddleware emits one structured log line per request.
func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &statusCapture{ResponseWriter: w, status: 200}
		next.ServeHTTP(capture, r)
		s.logger.Info("http.request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", capture.status),
			slog.String("request_id", RequestIDFromContext(r.Context())),
		)
	})
}

// recoverMiddleware converts panics into a 500 problem+json response.
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("http.panic",
					slog.Any("panic", rec),
					slog.String("path", r.URL.Path),
					slog.String("request_id", RequestIDFromContext(r.Context())),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (c *statusCapture) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}
