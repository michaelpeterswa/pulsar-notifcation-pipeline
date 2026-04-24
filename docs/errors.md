# HTTP error catalogue

Every non-2xx response from the writer is an RFC 9457 `application/problem+json`
document (FR-022). This page lists every `type` URI the writer may emit, the HTTP
status it carries, what typically triggers it, and what an operator should do
about it.

All `type` URIs share a stable base of
`https://pulsar-notifcation-pipeline.example/problems/` (placeholder hostname;
the string values are stable regardless of where you deploy). Clients match on
the URI, not on the hostname — do not reconfigure the base in your consumer code.

## Catalogue

### `validation-failed` — HTTP 400

**Trigger**: the submitted canonical message does not match the schema (missing
required field, field out of range, malformed user key, etc.). See
[spec.md](../specs/001-notification-pipeline/spec.md) FR-003 and
[data-model.md](../specs/001-notification-pipeline/data-model.md) §2–§3, §7
for the exact rules.

**Extension**: the `errors` array carries one entry per failing field:

```json
"errors": [
  {
    "field": "content.priority",
    "code": "out-of-range",
    "message": "must be in [-2, 2]"
  }
]
```

**Operator action**: none. This is a caller problem. The request ID in the
`request_id` extension member lets you correlate with writer logs if needed.

**Client library**: `errors.Is(err, notificationclient.ErrValidationFailed)`.
`perr.FieldErrors()` enumerates the extension members.

---

### `authentication-required` — HTTP 401

**Trigger**: the request has no `Authorization` header, or the header value
is not a `Bearer <token>`.

**Operator action**: none. Caller must attach a bearer token.

**Client library**: `errors.Is(err, notificationclient.ErrAuthenticationRequired)`.

---

### `authentication-failed` — HTTP 401

**Trigger**: the supplied bearer token is not in
`WRITER_BEARER_TOKENS_FILE`.

**Operator action**: if the caller should be allowed, add their token to the
tokens file and restart the writer. If not, nothing to do — the reject is
intentional.

**Client library**: `errors.Is(err, notificationclient.ErrAuthenticationFailed)`.

---

### `idempotency-conflict` — HTTP 409

**Trigger**: the supplied `Idempotency-Key` header matches a recent request
but with a divergent payload.

> **MVP status**: FR-014 is specced but not wired end-to-end in the initial
> release. The writer does not currently store idempotency keys; it does
> forward the key to the canonical message so downstream consumers CAN
> deduplicate. This response type is reserved for when the key-store lands.

**Operator action**: N/A at MVP.

---

### `unsupported-content-type` — HTTP 415

**Trigger**: `Content-Type` is neither `application/json` nor
`application/x-protobuf` (or `application/protobuf`).

**Operator action**: none. Caller must send one of the supported content
types.

**Client library**: `errors.Is(err, notificationclient.ErrUnsupportedContentType)`.

---

### `substrate-unavailable` — HTTP 503

**Trigger**: the writer could not publish to Pulsar (broker connection error,
producer disconnected, etc.). The writer classifies all publish errors as
transient and surfaces 503 with a `Retry-After: 5` header.

**Operator action**: this is typically a Pulsar problem. Check broker health,
connectivity from the writer's pod/host, and any topic-level policy changes
(e.g., max producers, rate limits). Once the substrate recovers, callers
retrying will succeed.

**Client library**: `errors.Is(err, notificationclient.ErrSubstrateUnavailable)`.

---

### `internal` — HTTP 500

**Trigger**: any unexpected error the writer could not categorise (marshaling
bug, nil pointer, etc.). The `detail` field is intentionally generic; the
writer never leaks internals in this body.

**Operator action**: examine writer logs for the `request_id` from the
response. These should be vanishingly rare in production — treat as an
incident.

**Client library**: `errors.Is(err, notificationclient.ErrInternal)`.

---

## Extension members common to every problem

| Member | Type | Meaning |
|--------|------|---------|
| `request_id` | string | Server-generated ULID for request-scope correlation. Mirrors the `X-Request-Id` response header and the `meta.request_id` on successful responses. Always present. |
| `errors` | array | Per-field error list. Only present on `validation-failed`. |

## How the client library maps problem types to Go errors

```go
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
```

Match categories with `errors.Is`; recover the full `*ProblemError` with
`errors.As` for access to `Type()`, `Status()`, `Title()`, `Detail()`,
`Instance()`, `RequestID()`, `FieldErrors()`.
