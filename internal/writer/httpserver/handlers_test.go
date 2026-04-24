package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/pulsarlib"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/httpserver"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/ingest"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

type fakePublisher struct {
	err     error
	lastIn  pulsarlib.PublishInput
	callCnt int
}

func (f *fakePublisher) Publish(_ context.Context, in pulsarlib.PublishInput) error {
	f.callCnt++
	f.lastIn = in
	return f.err
}
func (f *fakePublisher) Close() error { return nil }

func validNotification() *notificationpbv1.Notification {
	return &notificationpbv1.Notification{
		Content: &notificationpbv1.Content{Title: "hi", Body: "there"},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{
				UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
			},
		},
	}
}

func newHandlers(t *testing.T, pub pulsarlib.Publisher) *httpserver.Handlers {
	t.Helper()
	ing, err := ingest.NewIngester(
		ingest.WithPublisher(pub),
		ingest.WithIDFunc(func() (string, error) { return "fixed-id", nil }),
	)
	if err != nil {
		t.Fatalf("ingest.NewIngester: %v", err)
	}
	h, err := httpserver.NewHandlers(
		httpserver.WithIngester(ing),
		httpserver.WithRequestIDExtractor(func(r *http.Request) string {
			if rid := r.Header.Get("X-Request-Id"); rid != "" {
				return rid
			}
			return "rid-test"
		}),
	)
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}
	return h
}

// We exercise the real auth middleware with a stub Authenticator rather than
// forging a context — that keeps the context-key unexported in auth/ and
// tests the middleware → handler interplay end-to-end.

type stubAuth struct{ name string }

// Authenticate behaves like a minimal bearer checker: any non-empty Bearer
// token passes and attaches the configured ServiceName; a missing header
// returns ErrNoCredentials so the auth middleware emits the correct 401.
func (s stubAuth) Authenticate(_ context.Context, r *http.Request) (auth.Principal, error) {
	if auth.BearerToken(r) == "" {
		return auth.Principal{}, auth.ErrNoCredentials
	}
	return auth.Principal{ServiceName: s.name}, nil
}

func newServer(t *testing.T, pub pulsarlib.Publisher, svcName string) http.Handler {
	t.Helper()
	h := newHandlers(t, pub)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.SubmitNotification(w, r, httpserver.SubmitNotificationParams{})
	})
	return auth.Middleware(stubAuth{name: svcName}, inner)
}

func TestSubmitJSON202(t *testing.T) {
	pub := &fakePublisher{}
	srv := newServer(t, pub, "svc-a")

	body, err := protojson.Marshal(validNotification())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any")
	req.Header.Set("X-Request-Id", "rid-q")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Result().StatusCode != 202 {
		t.Fatalf("status = %d body=%s", rr.Result().StatusCode, rr.Body.String())
	}
	var env map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := env["data"].(map[string]any)
	if data["notification_id"] != "fixed-id" {
		t.Errorf("notification_id = %v", data["notification_id"])
	}
	if data["accepted_at"] == "" {
		t.Error("accepted_at missing")
	}
	meta := env["meta"].(map[string]any)
	if meta["request_id"] != "rid-q" {
		t.Errorf("meta.request_id = %v", meta["request_id"])
	}
	if pub.callCnt != 1 {
		t.Errorf("publisher calls = %d", pub.callCnt)
	}
	if pub.lastIn.NotificationID != "fixed-id" {
		t.Errorf("published notification_id = %q", pub.lastIn.NotificationID)
	}
}

func TestSubmitProtobuf202(t *testing.T) {
	pub := &fakePublisher{}
	srv := newServer(t, pub, "svc-a")

	body, err := proto.Marshal(validNotification())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Result().StatusCode != 202 {
		t.Fatalf("status = %d body=%s", rr.Result().StatusCode, rr.Body.String())
	}
	if ct := rr.Result().Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("response content-type = %q (must always be JSON)", ct)
	}
}

func TestSubmitValidationFailed(t *testing.T) {
	pub := &fakePublisher{}
	srv := newServer(t, pub, "svc-a")

	bad := validNotification()
	bad.Content.Priority = 9 // out of range
	body, _ := protojson.Marshal(bad)
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Result().StatusCode != 400 {
		t.Fatalf("status = %d", rr.Result().StatusCode)
	}
	if ct := rr.Result().Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(b), "validation-failed") {
		t.Errorf("body does not name type: %s", b)
	}
	if !strings.Contains(string(b), "out-of-range") {
		t.Errorf("body does not name field code: %s", b)
	}
}

func TestSubmitUnsupportedContentType(t *testing.T) {
	pub := &fakePublisher{}
	srv := newServer(t, pub, "svc-a")
	req := httptest.NewRequest(http.MethodPost, "/notifications", strings.NewReader("..."))
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Result().StatusCode != 415 {
		t.Fatalf("status = %d", rr.Result().StatusCode)
	}
}

func TestSubmitSubstrateUnavailable(t *testing.T) {
	pub := &fakePublisher{err: &pulsarlib.PublishError{Transient: true, Err: errors.New("pulsar nope")}}
	srv := newServer(t, pub, "svc-a")

	body, _ := protojson.Marshal(validNotification())
	req := httptest.NewRequest(http.MethodPost, "/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Result().StatusCode != 503 {
		t.Fatalf("status = %d", rr.Result().StatusCode)
	}
	if ra := rr.Result().Header.Get("Retry-After"); ra == "" {
		t.Error("Retry-After missing")
	}
	b, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(b), "substrate-unavailable") {
		t.Errorf("body does not name type: %s", b)
	}
}
