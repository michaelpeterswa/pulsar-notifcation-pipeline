package pushover_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider"
	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/deliverer/provider/pushover"
	notificationpbv1 "github.com/michaelpeterswa/pulsar-notifcation-pipeline/pkg/notificationpb/v1"
)

func notif() *notificationpbv1.Notification {
	return &notificationpbv1.Notification{
		NotificationId: "nid-1",
		Content: &notificationpbv1.Content{
			Title: "Hello",
			Body:  "World",
		},
		Target: &notificationpbv1.Notification_Pushover{
			Pushover: &notificationpbv1.PushoverTarget{
				UserOrGroupKey: "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
			},
		},
	}
}

func newProviderAgainst(t *testing.T, handler http.HandlerFunc) (*pushover.Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := pushover.New(
		pushover.WithAppToken("app-token"),
		pushover.WithBaseURL(srv.URL),
		pushover.WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return p, srv
}

func TestNewRequiresAppToken(t *testing.T) {
	if _, err := pushover.New(); err == nil {
		t.Error("expected missing-app-token error")
	}
}

func TestPushSuccess(t *testing.T) {
	var gotForm string
	p, _ := newProviderAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotForm = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	})
	res, err := p.Push(context.Background(), notif())
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if !strings.Contains(gotForm, "token=app-token") {
		t.Errorf("app token not in form: %s", gotForm)
	}
	if !strings.Contains(gotForm, "user=uQiRzpo4DXghDmr9QzzfQu27cmVRsG") {
		t.Errorf("user not in form: %s", gotForm)
	}
	if !strings.Contains(gotForm, "title=Hello") {
		t.Errorf("title not in form: %s", gotForm)
	}
}

func TestPushClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSent   error
		wantReason string
	}{
		{"400 recipient", 400, `{"status":0,"errors":["user key is invalid"]}`, provider.ErrPermanent, "provider-rejected-recipient"},
		{"400 payload", 400, `{"status":0,"errors":["message too long"]}`, provider.ErrPermanent, "provider-rejected-payload"},
		{"401 auth", 401, `{"status":0}`, provider.ErrPermanent, "provider-auth-failed"},
		{"403 auth", 403, `{"status":0}`, provider.ErrPermanent, "provider-auth-failed"},
		{"429 transient", 429, `{"status":0}`, provider.ErrTransient, ""},
		{"500 transient", 500, `{"status":0}`, provider.ErrTransient, ""},
		{"502 transient", 502, `{"status":0}`, provider.ErrTransient, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newProviderAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := p.Push(context.Background(), notif())
			if !errors.Is(err, tc.wantSent) {
				t.Fatalf("error does not match sentinel: got %v want %v", err, tc.wantSent)
			}
			if tc.wantReason != "" {
				var pe *provider.PermanentError
				if !errors.As(err, &pe) {
					t.Fatalf("expected *PermanentError, got %T", err)
				}
				if pe.Reason != tc.wantReason {
					t.Errorf("reason = %q, want %q", pe.Reason, tc.wantReason)
				}
			}
		})
	}
}

func TestPushNilTarget(t *testing.T) {
	p, _ := newProviderAgainst(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be reached for malformed input")
	})
	bad := notif()
	bad.Target = nil
	_, err := p.Push(context.Background(), bad)
	if !errors.Is(err, provider.ErrPermanent) {
		t.Errorf("expected permanent error, got %v", err)
	}
}

func TestPushName(t *testing.T) {
	p, _ := newProviderAgainst(t, func(_ http.ResponseWriter, _ *http.Request) {})
	if p.Name() != "pushover" {
		t.Errorf("Name = %q", p.Name())
	}
}
