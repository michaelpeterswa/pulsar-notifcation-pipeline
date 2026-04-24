package auth_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/auth"
)

func writeTokens(t *testing.T, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestNewBearerRequiresPath(t *testing.T) {
	if _, err := auth.NewBearer(); err == nil {
		t.Error("expected error without WithTokensFile")
	}
}

func TestNewBearerLoadsTokens(t *testing.T) {
	p := writeTokens(t, "# a comment\n\nabc svc-a\ndef svc-b\nghi\n")
	b, err := auth.NewBearer(auth.WithTokensFile(p))
	if err != nil {
		t.Fatalf("NewBearer: %v", err)
	}

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer abc")
	pr, err := b.Authenticate(context.Background(), req)
	if err != nil {
		t.Fatalf("Authenticate(abc): %v", err)
	}
	if pr.ServiceName != "svc-a" {
		t.Errorf("ServiceName = %q", pr.ServiceName)
	}

	req.Header.Set("Authorization", "Bearer ghi")
	pr, err = b.Authenticate(context.Background(), req)
	if err != nil {
		t.Fatalf("Authenticate(ghi): %v", err)
	}
	if pr.ServiceName != "unknown" {
		t.Errorf("ServiceName = %q (expected fallback 'unknown')", pr.ServiceName)
	}

	req.Header.Set("Authorization", "Bearer zzz")
	_, err = b.Authenticate(context.Background(), req)
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}

	req.Header.Del("Authorization")
	_, err = b.Authenticate(context.Background(), req)
	if !errors.Is(err, auth.ErrNoCredentials) {
		t.Errorf("expected ErrNoCredentials, got %v", err)
	}
}

func TestNewBearerEmptyFileRejected(t *testing.T) {
	p := writeTokens(t, "# only comments\n\n")
	if _, err := auth.NewBearer(auth.WithTokensFile(p)); err == nil {
		t.Error("expected error for empty tokens file")
	}
}

func TestNewBearerMissingFile(t *testing.T) {
	if _, err := auth.NewBearer(auth.WithTokensFile("/no/such/file")); err == nil {
		t.Error("expected error for missing file")
	}
}
