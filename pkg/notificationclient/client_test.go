package notificationclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationclient"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func writeSuccess(w http.ResponseWriter, reqID, nid string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"notification_id": nid,
			"accepted_at":     "2026-04-23T18:00:00.123Z",
		},
		"meta": map[string]any{"request_id": reqID},
	})
}

func writeProblem(w http.ResponseWriter, typeURI string, status int, fields []map[string]string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	payload := map[string]any{
		"type":       typeURI,
		"title":      "boom",
		"status":     status,
		"detail":     "something went wrong",
		"instance":   "/notifications",
		"request_id": "req-X",
	}
	if fields != nil {
		payload["errors"] = fields
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func TestSubmitSuccess(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc" {
			t.Errorf("missing bearer: %v", r.Header)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		writeSuccess(w, "req-42", "nid-1")
	})

	c, err := notificationclient.New(srv.URL, notificationclient.WithBearerToken("abc"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.NotificationID() != "nid-1" {
		t.Errorf("NotificationID = %q", resp.NotificationID())
	}
	if resp.RequestID() != "req-42" {
		t.Errorf("RequestID = %q", resp.RequestID())
	}
	if resp.AcceptedAt().IsZero() {
		t.Error("AcceptedAt zero")
	}
}

func TestSubmitValidationFailed(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, "https://pulsar-notifcation-pipeline.example/problems/validation-failed",
			400, []map[string]string{{"field": "content.priority", "code": "out-of-range"}})
	})

	c, _ := notificationclient.New(srv.URL)
	_, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, notificationclient.ErrValidationFailed) {
		t.Errorf("errors.Is(err, ErrValidationFailed) = false")
	}
	var perr *notificationclient.ProblemError
	if !errors.As(err, &perr) {
		t.Fatalf("errors.As failed: %T", err)
	}
	if perr.Status() != 400 {
		t.Errorf("Status = %d", perr.Status())
	}
	if perr.RequestID() != "req-X" {
		t.Errorf("RequestID = %q", perr.RequestID())
	}
	fes := perr.FieldErrors()
	if len(fes) != 1 || fes[0].Field != "content.priority" || fes[0].Code != "out-of-range" {
		t.Errorf("FieldErrors = %+v", fes)
	}
}

func TestSubmitAuthFailed(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, "https://pulsar-notifcation-pipeline.example/problems/authentication-failed", 401, nil)
	})
	c, _ := notificationclient.New(srv.URL)
	_, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
	if !errors.Is(err, notificationclient.ErrAuthenticationFailed) {
		t.Errorf("errors.Is(err, ErrAuthenticationFailed) = false (err=%v)", err)
	}
}

func TestSubmitSubstrateUnavailable(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		writeProblem(w, "https://pulsar-notifcation-pipeline.example/problems/substrate-unavailable", 503, nil)
	})
	c, _ := notificationclient.New(srv.URL)
	_, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
	if !errors.Is(err, notificationclient.ErrSubstrateUnavailable) {
		t.Errorf("errors.Is(err, ErrSubstrateUnavailable) = false (err=%v)", err)
	}
}

func TestSubmitUnknownProblem(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, "about:blank", 418, nil)
	})
	c, _ := notificationclient.New(srv.URL)
	_, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b"))
	if !errors.Is(err, notificationclient.ErrUnknownProblem) {
		t.Errorf("errors.Is(err, ErrUnknownProblem) = false (err=%v)", err)
	}
}

func TestSubmitProtobufContentType(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/x-protobuf" {
			t.Errorf("content-type = %q", ct)
		}
		writeSuccess(w, "req-p", "nid-p")
	})
	c, _ := notificationclient.New(srv.URL,
		notificationclient.WithContentType(notificationclient.ContentTypeProtobuf),
	)
	if _, err := c.Submit(context.Background(), notificationclient.NewPushoverRequest(
		"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
}

func TestSubmitIdempotencyHeaderPropagated(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "client-key-1" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		writeSuccess(w, "req", "nid")
	})
	c, _ := notificationclient.New(srv.URL)
	req := notificationclient.NewPushoverRequest("uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "t", "b")
	req.IdempotencyKey = "client-key-1"
	if _, err := c.Submit(context.Background(), req); err != nil {
		t.Fatalf("Submit: %v", err)
	}
}

func TestSubmitRejectsNilReq(t *testing.T) {
	c, _ := notificationclient.New("http://localhost:0")
	if _, err := c.Submit(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "nil request") {
		t.Errorf("expected nil-request error, got %v", err)
	}
}

func TestNewRequiresBaseURL(t *testing.T) {
	if _, err := notificationclient.New(""); err == nil {
		t.Error("expected missing-baseURL error")
	}
}
