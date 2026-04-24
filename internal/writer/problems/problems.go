// Package problems is the writer's RFC 9457 "Problem Details for HTTP APIs"
// emission layer, wrapping alpineworks.io/rfc9457 with a typed catalogue of
// the errors the writer may return (FR-022, SC-014). Every handler-visible
// failure funnels through this package so that error shape is uniform.
//
// See research.md §8 for the canonical `type` URI catalogue.
package problems

import (
	"encoding/json"
	"net/http"

	"alpineworks.io/rfc9457"
)

// TypeBase is the URI prefix for all categorised problem types. The host is a
// placeholder until the project has a deployment URL; the `type` values are
// stable regardless (FR-022).
const TypeBase = "https://pulsar-notifcation-pipeline.example/problems/"

// URIs for the documented catalogue.
const (
	TypeValidationFailed       = TypeBase + "validation-failed"
	TypeAuthenticationRequired = TypeBase + "authentication-required"
	TypeAuthenticationFailed   = TypeBase + "authentication-failed"
	TypeIdempotencyConflict    = TypeBase + "idempotency-conflict"
	TypeUnsupportedContentType = TypeBase + "unsupported-content-type"
	TypeSubstrateUnavailable   = TypeBase + "substrate-unavailable"
	TypeInternal               = TypeBase + "internal"
)

// FieldError is the extension-member shape used on validation-failed problems.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Problem is a writer-flavoured problem document. It embeds the alpineworks
// type so the RFC 9457 fields are authoritative, and it adds the request-id
// and field-errors extension members that this API emits (FR-021, FR-022).
type Problem struct {
	rfc9457.RFC9457
	RequestID string       `json:"request_id"`
	Errors    []FieldError `json:"errors,omitempty"`
}

// ContentType is the canonical media type for RFC 9457 problem responses.
const ContentType = rfc9457.ProblemContentType

// Write serialises p to w with the correct content type and status code.
func Write(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

type builder struct {
	typeURI   string
	title     string
	status    int
	detail    string
	instance  string
	requestID string
	errors    []FieldError
}

func (b builder) build() *Problem {
	r := rfc9457.NewRFC9457(
		rfc9457.WithType(b.typeURI),
		rfc9457.WithTitle(b.title),
		rfc9457.WithStatus(b.status),
		rfc9457.WithDetail(b.detail),
		rfc9457.WithInstance(b.instance),
	)
	return &Problem{RFC9457: *r, RequestID: b.requestID, Errors: b.errors}
}

// ValidationFailed constructs a 400 problem for canonical-schema violations.
func ValidationFailed(requestID, instance, detail string, errs []FieldError) *Problem {
	return builder{
		typeURI:   TypeValidationFailed,
		title:     "The submitted notification failed validation.",
		status:    http.StatusBadRequest,
		detail:    detail,
		instance:  instance,
		requestID: requestID,
		errors:    errs,
	}.build()
}

// AuthenticationRequired constructs a 401 problem for missing credentials.
func AuthenticationRequired(requestID, instance string) *Problem {
	return builder{
		typeURI:   TypeAuthenticationRequired,
		title:     "Authentication is required.",
		status:    http.StatusUnauthorized,
		detail:    "Supply a bearer token via the Authorization header.",
		instance:  instance,
		requestID: requestID,
	}.build()
}

// AuthenticationFailed constructs a 401 problem for rejected credentials.
func AuthenticationFailed(requestID, instance string) *Problem {
	return builder{
		typeURI:   TypeAuthenticationFailed,
		title:     "Authentication failed.",
		status:    http.StatusUnauthorized,
		detail:    "The supplied bearer token is not recognised.",
		instance:  instance,
		requestID: requestID,
	}.build()
}

// IdempotencyConflict constructs a 409 problem for divergent-payload idempotency reuse.
func IdempotencyConflict(requestID, instance, detail string) *Problem {
	return builder{
		typeURI:   TypeIdempotencyConflict,
		title:     "Idempotency key reused with a divergent payload.",
		status:    http.StatusConflict,
		detail:    detail,
		instance:  instance,
		requestID: requestID,
	}.build()
}

// UnsupportedContentType constructs a 415 problem.
func UnsupportedContentType(requestID, instance, got string) *Problem {
	return builder{
		typeURI:   TypeUnsupportedContentType,
		title:     "Content-Type is not supported.",
		status:    http.StatusUnsupportedMediaType,
		detail:    "Content-Type must be application/json or application/x-protobuf; got " + got,
		instance:  instance,
		requestID: requestID,
	}.build()
}

// SubstrateUnavailable constructs a 503 problem. retryAfterSeconds, when
// positive, is surfaced via the standard Retry-After header in handlers.
func SubstrateUnavailable(requestID, instance, detail string) *Problem {
	return builder{
		typeURI:   TypeSubstrateUnavailable,
		title:     "The messaging substrate is temporarily unavailable.",
		status:    http.StatusServiceUnavailable,
		detail:    detail,
		instance:  instance,
		requestID: requestID,
	}.build()
}

// Internal constructs a 500 problem for unexpected server errors. Detail is
// intentionally generic so internals are not leaked.
func Internal(requestID, instance string) *Problem {
	return builder{
		typeURI:   TypeInternal,
		title:     "An unexpected server error occurred.",
		status:    http.StatusInternalServerError,
		detail:    "Please retry; contact operations if the condition persists.",
		instance:  instance,
		requestID: requestID,
	}.build()
}
