package notificationclient

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel errors, each representing one entry of the writer's RFC 9457
// catalogue (research.md §8, FR-024). Callers use errors.Is to identify
// the category; they use errors.As to access the full *ProblemError for
// per-field validation details.
var (
	ErrValidationFailed       = errors.New("notificationclient: validation-failed")
	ErrAuthenticationRequired = errors.New("notificationclient: authentication-required")
	ErrAuthenticationFailed   = errors.New("notificationclient: authentication-failed")
	ErrIdempotencyConflict    = errors.New("notificationclient: idempotency-conflict")
	ErrUnsupportedContentType = errors.New("notificationclient: unsupported-content-type")
	ErrSubstrateUnavailable   = errors.New("notificationclient: substrate-unavailable")
	ErrInternal               = errors.New("notificationclient: internal")
	ErrUnknownProblem         = errors.New("notificationclient: unknown-problem-type")
)

// ProblemError is the typed RFC 9457 problem document surfaced to callers.
// Use errors.As to unwrap from Submit's returned error.
type ProblemError struct {
	TypeURI  string
	title    string
	status   int
	detail   string
	instance string
	reqID    string
	fields   []FieldError

	// sentinel is the matching package-level sentinel, used by errors.Is to
	// support category-level checks.
	sentinel error
}

// FieldError is the shape of each entry in the `errors` extension member on
// validation-failed problem documents.
type FieldError struct {
	Field   string
	Code    string
	Message string
}

// Error implements error.
func (e *ProblemError) Error() string {
	if e == nil {
		return "notificationclient: nil problem"
	}
	if e.title != "" {
		return fmt.Sprintf("%s (status %d): %s — %s", e.sentinel, e.status, e.title, e.detail)
	}
	return fmt.Sprintf("%s (status %d)", e.sentinel, e.status)
}

// Is implements errors.Is for category checks.
func (e *ProblemError) Is(target error) bool {
	return target != nil && target == e.sentinel
}

// Type returns the RFC 9457 type URI.
func (e *ProblemError) Type() string { return e.TypeURI }

// Title returns the problem title.
func (e *ProblemError) Title() string { return e.title }

// Status returns the HTTP status code.
func (e *ProblemError) Status() int { return e.status }

// Detail returns the problem detail string.
func (e *ProblemError) Detail() string { return e.detail }

// Instance returns the problem instance URI.
func (e *ProblemError) Instance() string { return e.instance }

// RequestID returns the server-emitted request_id extension member.
func (e *ProblemError) RequestID() string { return e.reqID }

// FieldErrors returns the per-field validation errors, present only on
// validation-failed problems.
func (e *ProblemError) FieldErrors() []FieldError { return append([]FieldError(nil), e.fields...) }

type problemWire struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	RequestID string `json:"request_id"`
	Errors    []struct {
		Field   string `json:"field"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// decodeProblem parses an application/problem+json body into *ProblemError.
func decodeProblem(body []byte, fallbackStatus int) *ProblemError {
	var w problemWire
	_ = json.Unmarshal(body, &w)
	pe := &ProblemError{
		TypeURI:  w.Type,
		title:    w.Title,
		status:   w.Status,
		detail:   w.Detail,
		instance: w.Instance,
		reqID:    w.RequestID,
	}
	if pe.status == 0 {
		pe.status = fallbackStatus
	}
	for _, fe := range w.Errors {
		pe.fields = append(pe.fields, FieldError{Field: fe.Field, Code: fe.Code, Message: fe.Message})
	}
	pe.sentinel = sentinelFor(w.Type)
	return pe
}

func sentinelFor(typeURI string) error {
	switch typeURI {
	case "https://pulsar-notifcation-pipeline.example/problems/validation-failed":
		return ErrValidationFailed
	case "https://pulsar-notifcation-pipeline.example/problems/authentication-required":
		return ErrAuthenticationRequired
	case "https://pulsar-notifcation-pipeline.example/problems/authentication-failed":
		return ErrAuthenticationFailed
	case "https://pulsar-notifcation-pipeline.example/problems/idempotency-conflict":
		return ErrIdempotencyConflict
	case "https://pulsar-notifcation-pipeline.example/problems/unsupported-content-type":
		return ErrUnsupportedContentType
	case "https://pulsar-notifcation-pipeline.example/problems/substrate-unavailable":
		return ErrSubstrateUnavailable
	case "https://pulsar-notifcation-pipeline.example/problems/internal":
		return ErrInternal
	default:
		return ErrUnknownProblem
	}
}
