# Data Model: End-to-End Push Notification Pipeline

**Feature**: `001-notification-pipeline`
**Sources**: spec.md §Key Entities, research.md §2 (oneof design), §8 (error catalogue).

The pipeline is stateless at MVP (no database), so "data model" here means: the on-wire
shapes (protobuf for the Pulsar substrate and request bodies; JSON for HTTP responses), the
in-process domain types, and their validation rules.

All field numbers in this document are normative; changing one is a breaking change to
the protobuf library and requires a major-version bump per FR-013.

---

## 1. Notification (canonical Pulsar/HTTP message)

**Source of truth**: `proto/notification/v1/notification.proto` (see
`contracts/notification.proto`).

```proto
syntax = "proto3";
package notification.v1;

import "google/protobuf/timestamp.proto";

message Notification {
  string notification_id    = 1;  // Server-assigned by the writer. Stable across the pipeline.
  google.protobuf.Timestamp created_at = 2;  // Server-assigned by the writer.
  google.protobuf.Timestamp expires_at = 3;  // OPTIONAL. Caller-supplied. Zero = never.
  string originating_service = 4; // REQUIRED. Derived from the caller's bearer token.
  string idempotency_key     = 5; // OPTIONAL. Caller-supplied. Empty string = absent.

  Content content = 10;
  oneof target {
    PushoverTarget pushover = 100;
  }
}
```

**Field rules**:

| Field | Source | On input (request) | On wire (Pulsar) | Constraint |
|-------|--------|--------------------|------------------|------------|
| `notification_id` | writer | MUST be absent/empty | REQUIRED, server-assigned UUIDv7 | uniqueness within the pipeline |
| `created_at` | writer | MUST be absent | REQUIRED | UTC, truncated to milliseconds |
| `expires_at` | caller | OPTIONAL | as-is | MUST be ≥ `created_at` if set |
| `originating_service` | writer | MUST be absent | REQUIRED | non-empty; derived from auth |
| `idempotency_key` | caller | OPTIONAL, max 128 chars | as-is | if present, uniqueness enforced in the idempotency window (FR-014) |
| `content` | caller | REQUIRED | REQUIRED | validated per §2 |
| `target.pushover` | caller | REQUIRED (only arm for MVP) | REQUIRED | validated per §3 |

**Lifecycle**:

- *Accepted*: writer has validated, assigned ID, and successfully acknowledged to the
  upstream caller (implies Pulsar publish succeeded — FR-005 ties acknowledgement to
  publish).
- *In-flight*: on the Pulsar topic, ciphertext.
- *Consumed*: the reader/deliverer has read and decrypted.
- *Dispatched*: a push-provider call has started.
- *Terminal*: `delivered`, `permanently-failed`, or `expired`. See §4.

---

## 2. Content (provider-agnostic payload)

```proto
message Content {
  string title                = 1;  // REQUIRED. <= 250 chars.
  string body                 = 2;  // REQUIRED. <= 1024 chars.
  int32  priority             = 3;  // OPTIONAL. -2..+2. Default 0 (normal).
  map<string, string> data    = 4;  // OPTIONAL. Extra key/value metadata for the client.
}
```

**Validation rules** (enforced by the writer before publish, per FR-003):

- `title` — 1..250 UTF-8 chars; no leading/trailing whitespace.
- `body` — 1..1024 UTF-8 chars.
- `priority` — in the range `[-2, 2]`. Out-of-range → `validation-failed` problem
  (FR-022 / §6 below).
- `data` — ≤ 32 entries; each key ≤ 64 chars, each value ≤ 512 chars. Keys are
  case-sensitive, must match `^[a-zA-Z_][a-zA-Z0-9_]*$`.

**Rationale for length caps**: Pushover's 1024-byte message-body limit and 250-byte
title limit are the tightest known provider constraints; caps are set to match so
that cross-provider portability is preserved when a second provider arrives.

---

## 3. PushoverTarget (provider-specific addressing + options)

```proto
message PushoverTarget {
  string user_or_group_key = 1;  // REQUIRED. 30-char Pushover user/group key.
  string device_name       = 2;  // OPTIONAL. Specific device; empty = all devices.
  string sound             = 3;  // OPTIONAL. Pushover sound name; empty = default.
  string url               = 4;  // OPTIONAL. Supplementary URL shown with the message.
  string url_title         = 5;  // OPTIONAL. Text for `url`. Ignored if `url` empty.
}
```

**Validation rules** (enforced by the writer before publish):

- `user_or_group_key` — exactly 30 alphanumeric characters (Pushover format). The writer
  MUST NOT contact Pushover to validate; it checks the syntactic shape only. Actual key
  validity is checked by the deliverer at dispatch time, and a Pushover rejection maps
  to the `provider-rejected-recipient` terminal reason.
- `device_name` — 1..25 chars if present, `^[A-Za-z0-9_-]+$`.
- `sound` — ≤ 25 chars if present.
- `url` — valid absolute URL (RFC 3986) ≤ 512 chars if present.
- `url_title` — ≤ 100 chars if present; if set, `url` MUST also be set.

---

## 4. Delivery Outcome (in-process domain type on the deliverer)

Not on the wire. Emitted as a structured log event per notification.

```go
type Outcome struct {
    NotificationID string
    Status         Status            // delivered | permanently-failed | expired
    Reason         string            // optional reason code (see §5)
    ProviderResp   string            // optional raw provider response snippet (truncated)
    Attempts       int
    FirstAttempt   time.Time
    LastAttempt    time.Time
}

type Status int
const (
    StatusDelivered Status = iota + 1
    StatusPermanentlyFailed
    StatusExpired
)
```

**Recording rules**:

- Every consumed message produces exactly one `Outcome` row in the structured log,
  keyed on `notification_id`.
- `Reason` is required when `Status != StatusDelivered`.
- `ProviderResp` is truncated to 512 bytes; never logs credentials.

---

## 5. Reason codes

Canonical set referenced by multiple FRs:

| Code | Emitted by | Triggers |
|------|------------|----------|
| `encryption-policy-violation` | deliverer | FR-016: unencrypted or undecryptable message. |
| `provider-rejected-recipient` | deliverer | Pushover 400 with user/token error in `errors[]`. |
| `provider-rejected-payload`   | deliverer | Pushover 400 otherwise. |
| `provider-auth-failed`        | deliverer | Pushover 401/403 (operator misconfiguration). |
| `retry-budget-exhausted`      | deliverer | Transient errors exceeded `DELIVERER_RETRY_MAX_ATTEMPTS`. |
| `expired`                     | deliverer | `expires_at` in the past at consume time. |

Any change to this set is visible in logs/metrics; promote it to a typed Go enum in
`internal/deliverer/outcomes/` so misspellings are compile errors.

---

## 6. HTTP response envelope (writer, always JSON — FR-023)

### 6.1 Success envelope (FR-021)

```json
{
  "data": {
    "notification_id": "01933dcc-2b8f-7d44-9aab-52c6f1c0a5d7",
    "accepted_at": "2026-04-23T18:00:00.123Z"
  },
  "meta": {
    "request_id": "req_01HS98TQKJ5PXQZ5N8C3GD6G1N"
  }
}
```

- HTTP status: `202 Accepted`
- Content-Type: `application/json`
- `meta.request_id` is server-generated per request (ULID recommended). Distinct from
  `notification_id`; carries request-scope correlation for auth/decode failures where no
  notification was ever created.

### 6.2 Error envelope (FR-022, RFC 9457)

```json
{
  "type":     "https://pulsar-notifcation-pipeline.example/problems/validation-failed",
  "title":    "The submitted notification failed validation.",
  "status":   400,
  "detail":   "content.priority must be in [-2, 2]; got 5.",
  "instance": "/notifications",
  "request_id": "req_01HS98TQKJ5PXQZ5N8C3GD6G1N",
  "errors": [
    { "field": "content.priority", "code": "out-of-range", "message": "must be in [-2, 2]" }
  ]
}
```

- Content-Type: `application/problem+json`
- `type`, `title`, `status`, `detail`, `instance` — RFC 9457 required fields.
- `request_id` — extension member. Mirrors `meta.request_id` from 6.1 so operators can
  correlate a failed request to its server-side logs.
- `errors[]` — extension member. Present for `validation-failed`; one entry per failing
  field. `code` values are from a small closed set (see §7).

---

## 7. Validation error codes (extension on `validation-failed` problems)

| `field` | `code` values |
|---------|---------------|
| `content.title` | `required`, `too-long`, `whitespace-only` |
| `content.body`  | `required`, `too-long` |
| `content.priority` | `out-of-range` |
| `content.data`  | `too-many-entries`, `key-invalid`, `value-too-long` |
| `target` | `missing`, `unknown-arm` |
| `target.pushover.user_or_group_key` | `required`, `malformed` |
| `target.pushover.device_name` | `malformed`, `too-long` |
| `target.pushover.url` | `malformed`, `too-long` |
| `target.pushover.url_title` | `requires-url`, `too-long` |
| `expires_at` | `before-created-at` |
| `idempotency_key` | `too-long` |

---

## 8. Key material (Pulsar E2EE — FR-015, FR-017)

Not on the wire (keys never appear in messages). Operational data loaded at startup.

```go
type KeySet struct {
    Dir          string           // directory of PEM files
    ActiveNames  []string         // key names currently in rotation (e.g. ["v2", "v3"])
    Keys         map[string][]byte // name -> PEM bytes
}
```

- Writer's `KeySet` holds public keys; encrypts each outgoing message to all
  `ActiveNames`.
- Deliverer's `KeySet` holds private keys; decrypts with whichever key ID the message
  was encrypted under (Pulsar client selects automatically).
- File naming convention: `<key-name>.pub.pem` for public, `<key-name>.priv.pem` for
  private. Loaded via a `pulsar.CryptoKeyReader` implementation in
  `internal/pulsarlib/keys.go`.

---

## 9. Entity relationships (summary)

```text
┌─────────────────────┐      publish (ciphertext)       ┌────────────────────────┐
│  HTTP request body  │ ──── (Notification) ──────────▶ │    Pulsar topic         │
│  (protobuf or       │                                 │  (encrypted payload;    │
│   proto3 canonical  │                                 │  plaintext property:    │
│   JSON)             │                                 │   notification_id)      │
└─────────────────────┘                                 └────────────────────────┘
           │                                                       │
           │  writer assigns: notification_id,                     │  consume + decrypt
           │    created_at, originating_service                    ▼
           │                                          ┌────────────────────────┐
           ▼                                          │  Notification (clear)  │
┌─────────────────────┐                               └────────────────────────┘
│  HTTP response       │                                         │
│  ({data, meta} or    │                                         │  target.pushover
│  problem+json)       │                                         ▼
└─────────────────────┘                               ┌────────────────────────┐
                                                      │  PushProvider.Push()   │
                                                      │    (Pushover impl)     │
                                                      └────────────────────────┘
                                                                  │
                                                                  ▼
                                                      ┌────────────────────────┐
                                                      │  Outcome (structured   │
                                                      │  log; reason code)     │
                                                      └────────────────────────┘
```
