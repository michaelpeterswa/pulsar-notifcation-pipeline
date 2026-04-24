package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
)

type fakeAuth struct {
	principal auth.Principal
	err       error
}

func (f fakeAuth) Authenticate(_ context.Context, _ *http.Request) (auth.Principal, error) {
	return f.principal, f.err
}

func TestMiddlewareCallsNextOnSuccess(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			t.Error("Principal missing from context")
		}
		if p.ServiceName != "svc-x" {
			t.Errorf("ServiceName = %q", p.ServiceName)
		}
		w.WriteHeader(201)
	})
	h := auth.Middleware(fakeAuth{principal: auth.Principal{ServiceName: "svc-x"}}, inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	h.ServeHTTP(rr, req)
	if rr.Result().StatusCode != 201 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}
}

func TestMiddlewareEmitsAuthRequired(t *testing.T) {
	h := auth.Middleware(fakeAuth{err: auth.ErrNoCredentials}, http.NotFoundHandler())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	h.ServeHTTP(rr, req)
	if rr.Result().StatusCode != 401 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}
	if ct := rr.Result().Header.Get("Content-Type"); ct != problems.ContentType {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "authentication-required") {
		t.Errorf("body does not name the error: %s", rr.Body.String())
	}
}

func TestMiddlewareEmitsAuthFailed(t *testing.T) {
	h := auth.Middleware(fakeAuth{err: auth.ErrInvalidCredentials}, http.NotFoundHandler())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	h.ServeHTTP(rr, req)
	if rr.Result().StatusCode != 401 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}
	if !strings.Contains(rr.Body.String(), "authentication-failed") {
		t.Errorf("body does not name the error: %s", rr.Body.String())
	}
}

func TestMiddlewareUsesRequestIDFunc(t *testing.T) {
	h := auth.Middleware(fakeAuth{err: auth.ErrNoCredentials}, http.NotFoundHandler(),
		auth.WithRequestIDFunc(func(_ *http.Request) string { return "rid-xyz" }))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	h.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "rid-xyz") {
		t.Errorf("request_id not propagated: %s", rr.Body.String())
	}
}

func TestBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer abc123")
	if got := auth.BearerToken(req); got != "abc123" {
		t.Errorf("BearerToken = %q", got)
	}
	req.Header.Set("Authorization", "Basic zzz")
	if got := auth.BearerToken(req); got != "" {
		t.Errorf("non-bearer token should return empty: %q", got)
	}
}
