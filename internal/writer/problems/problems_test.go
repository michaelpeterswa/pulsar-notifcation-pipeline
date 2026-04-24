package problems_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelpeterswa/pulsar-notifcation-pipeline/internal/writer/problems"
)

func TestValidationFailed(t *testing.T) {
	p := problems.ValidationFailed("req-1", "/notifications", "priority out of range",
		[]problems.FieldError{{Field: "content.priority", Code: "out-of-range"}})
	if p.Type != problems.TypeValidationFailed {
		t.Errorf("Type = %q", p.Type)
	}
	if p.Status != 400 {
		t.Errorf("Status = %d", p.Status)
	}
	if p.RequestID != "req-1" {
		t.Errorf("RequestID = %q", p.RequestID)
	}
	if len(p.Errors) != 1 || p.Errors[0].Code != "out-of-range" {
		t.Errorf("Errors = %+v", p.Errors)
	}
}

func TestEveryCatalogueEntryShape(t *testing.T) {
	cases := []struct {
		name   string
		make   func() *problems.Problem
		typeID string
		status int
	}{
		{"auth-required", func() *problems.Problem { return problems.AuthenticationRequired("r", "/x") },
			problems.TypeAuthenticationRequired, 401},
		{"auth-failed", func() *problems.Problem { return problems.AuthenticationFailed("r", "/x") },
			problems.TypeAuthenticationFailed, 401},
		{"idempotency", func() *problems.Problem { return problems.IdempotencyConflict("r", "/x", "detail") },
			problems.TypeIdempotencyConflict, 409},
		{"unsupported-ct", func() *problems.Problem { return problems.UnsupportedContentType("r", "/x", "text/xml") },
			problems.TypeUnsupportedContentType, 415},
		{"substrate", func() *problems.Problem { return problems.SubstrateUnavailable("r", "/x", "pulsar not ready") },
			problems.TypeSubstrateUnavailable, 503},
		{"internal", func() *problems.Problem { return problems.Internal("r", "/x") },
			problems.TypeInternal, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.make()
			if p.Type != tc.typeID {
				t.Errorf("Type = %q want %q", p.Type, tc.typeID)
			}
			if p.Status != tc.status {
				t.Errorf("Status = %d want %d", p.Status, tc.status)
			}
			if p.Title == "" {
				t.Error("Title is empty")
			}
			if p.Detail == "" {
				t.Error("Detail is empty")
			}
			if p.Instance == "" {
				t.Error("Instance is empty")
			}
			if p.RequestID != "r" {
				t.Errorf("RequestID = %q", p.RequestID)
			}
		})
	}
}

func TestWriteEmitsProblemJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	p := problems.ValidationFailed("req-7", "/notifications", "bad", nil)
	problems.Write(rr, p)

	if got := rr.Result().Header.Get("Content-Type"); got != problems.ContentType {
		t.Errorf("content-type = %q", got)
	}
	if rr.Result().StatusCode != 400 {
		t.Errorf("status = %d", rr.Result().StatusCode)
	}
	var decoded map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if !strings.HasPrefix(decoded["type"].(string), "https://") {
		t.Errorf("type did not marshal as URI: %v", decoded["type"])
	}
	if decoded["request_id"] != "req-7" {
		t.Errorf("request_id = %v", decoded["request_id"])
	}
}

func TestTypeURIsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, uri := range []string{
		problems.TypeValidationFailed,
		problems.TypeAuthenticationRequired,
		problems.TypeAuthenticationFailed,
		problems.TypeIdempotencyConflict,
		problems.TypeUnsupportedContentType,
		problems.TypeSubstrateUnavailable,
		problems.TypeInternal,
	} {
		if seen[uri] {
			t.Errorf("duplicate type URI: %q", uri)
		}
		seen[uri] = true
	}
}
