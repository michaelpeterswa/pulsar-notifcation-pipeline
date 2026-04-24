package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
)

func newTestServer(t *testing.T) *httpserver.Server {
	t.Helper()
	pub := &fakePublisher{}
	ing, err := ingest.NewIngester(
		ingest.WithPublisher(pub),
		ingest.WithIDFunc(func() (string, error) { return "fixed-id", nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := httpserver.NewHandlers(
		httpserver.WithIngester(ing),
		httpserver.WithRequestIDExtractor(func(r *http.Request) string {
			return httpserver.RequestIDFromContext(r.Context())
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	srv, err := httpserver.NewServer(
		httpserver.WithHandler(hs),
		httpserver.WithAuthenticator(stubAuth{name: "svc-a"}),
		httpserver.WithLogger(logger),
		httpserver.WithAddress(":0"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestRouterHealthzUnauthenticated(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Result().StatusCode != 204 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}
}

func TestRouterRequestIDEchoedOnSuccess(t *testing.T) {
	srv := newTestServer(t)

	body, _ := protojson.Marshal(validNotification())
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	rid := rr.Result().Header.Get("X-Request-Id")
	if rid == "" {
		t.Fatal("X-Request-Id header missing on response")
	}

	var env map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	meta := env["meta"].(map[string]any)
	if meta["request_id"] != rid {
		t.Errorf("meta.request_id (%q) != header X-Request-Id (%q)", meta["request_id"], rid)
	}
}

func TestRouterRequestIDEchoedOnProblem(t *testing.T) {
	srv := newTestServer(t)

	// Send no Authorization, which auth.Middleware rejects with a problem.
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Result().StatusCode != 401 {
		t.Fatalf("status = %d", rr.Result().StatusCode)
	}
	var decoded map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&decoded)
	hdr := rr.Result().Header.Get("X-Request-Id")
	if hdr == "" {
		t.Fatal("X-Request-Id header missing on problem response")
	}
	if decoded["request_id"] != hdr {
		t.Errorf("problem.request_id (%q) != header X-Request-Id (%q)", decoded["request_id"], hdr)
	}
}

func TestRouterHonoursIncomingRequestID(t *testing.T) {
	srv := newTestServer(t)

	body, _ := protojson.Marshal(validNotification())
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any")
	req.Header.Set("X-Request-Id", "caller-supplied-rid")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if hdr := rr.Result().Header.Get("X-Request-Id"); hdr != "caller-supplied-rid" {
		t.Errorf("X-Request-Id = %q (caller-supplied should win)", hdr)
	}
}

// Stub authenticator (reused across tests in this package). Defined here so
// the router_test file has access without depending on symbols in the other
// test files.
var _ auth.Authenticator = stubAuth{}

// Ensure the signature works without the ingest import pruning.
var _ = context.Canceled
