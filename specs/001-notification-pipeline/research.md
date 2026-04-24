# Phase 0 Research: End-to-End Push Notification Pipeline

**Feature**: `001-notification-pipeline`
**Inputs**: spec.md, constitution.md v1.0.0, user planning note (Go 1.25+, Pulsar-only runtime
dep, gorilla/mux, Pushover first, interface-preserved for future providers, payload-carried
per-provider fields).

This document resolves every planning-time unknown so that Phase 1 design (data-model,
contracts, quickstart) can proceed without blocking questions.

---

## 1. Protobuf generator toolchain

**Decision**: `protoc` with `google.golang.org/protobuf/cmd/protoc-gen-go` (stdlib-blessed
Go generator), invoked through a `Makefile` target. No `buf` for the MVP.

**Rationale**:

- `protoc-gen-go` is the canonical, well-supported Go generator; the output matches what
  every other Go library expects.
- `buf` is a nice quality-of-life layer (lint, breaking-change detection, remote plugins)
  but it is an additional developer-machine dependency and `buf.build` infrastructure. The
  constitution's dependency-hygiene clause makes the default "no" on optional deps until
  we feel the pain.
- Breaking-change detection for FR-013's forward/backward-compat requirement can be
  achieved with a tiny Go test that loads both the current and the previous tag of
  `notification.proto` into two `protoreflect.FileDescriptor` instances and asserts:
  (a) no field numbers reused with different types, (b) no required-fields removed, (c) no
  enum values renamed, (d) no shrinking of oneof arms. This is simpler than wiring `buf`.

**Alternatives considered**:

- `buf` (rejected — not needed at MVP; revisit if schema churn justifies it).
- Hand-written Go types (rejected — FR-001 requires a real protobuf library; hand-written
  types would leave no wire definition).

---

## 2. Per-provider field carriage in the canonical message

This is the user's explicit "think about how to set the keys and such in the body of the
payload" ask.

**Decision**: Protobuf `oneof target` with one arm per push provider. At MVP the only
arm is `PushoverTarget`. Adding a future provider = adding a sibling message and a new
oneof arm with a fresh field number.

```proto
message Notification {
  string notification_id = 1;
  google.protobuf.Timestamp created_at = 2;
  google.protobuf.Timestamp expires_at = 3;
  string originating_service = 4;
  string idempotency_key = 5;  // optional; empty string = absent

  Content content = 10;        // Common, provider-agnostic content (title, body, ...)

  oneof target {               // Per-provider addressing + per-provider options
    PushoverTarget pushover = 100;
    // future: ApnsTarget   apns   = 101;
    // future: FcmTarget    fcm    = 102;
    // future: WebPushTarget webpush = 103;
  }
}

message Content {
  string title = 1;
  string body  = 2;
  int32  priority = 3;           // -2..2 (Pushover range; providers map down as needed)
  map<string, string> data = 4;  // Caller-supplied key/value pairs for deep-linking
}

message PushoverTarget {
  string user_or_group_key = 1;  // REQUIRED
  string device_name       = 2;  // OPTIONAL — specific device
  string sound             = 3;  // OPTIONAL — Pushover sound name
  string url               = 4;  // OPTIONAL — supplementary URL
  string url_title         = 5;  // OPTIONAL — text for the URL
}
```

**Rationale**:

- Idiomatic protobuf for "discriminated union of targets".
- **Wire-forward compatible**: a reader built against a library that doesn't know
  `ApnsTarget` (arm 101) reads such a message and sees `target = nil` (unknown oneof
  arms are preserved as unknown fields; Go accessors return zero). A reader built against
  a NEWER library reading an OLDER message (which only sets `pushover`) sees exactly
  the arm it expects. That satisfies FR-013 / SC-007.
- **Type-safe for the deliverer's dispatcher**: the dispatcher switches on the populated
  oneof arm via `pbmsg.GetTarget().(type)` and routes to the matching `PushProvider`. No
  runtime map lookup, no string discriminator.
- **The OpenAPI mapping is clean**: proto3 canonical JSON renders a oneof as a JSON object
  where exactly one key matches the arm name (e.g., `{"pushover": {...}}`). This maps
  directly to OpenAPI 3.1 `oneOf` with a `discriminator`, which `oapi-codegen` handles.
- **Keys live in the payload, not in deliverer config**: the Pushover *user/group* key is a
  per-message recipient identifier carried on `PushoverTarget.user_or_group_key`; the
  Pushover *application* API token (sender credential) is deliverer config via env
  (`PUSHOVER_APP_TOKEN`). That split is correct — recipient identity travels with the
  message, sender credentials do not.

**Alternatives considered**:

- `google.protobuf.Any` — rejected: stringly-typed URLs, worse IDE support, worse OpenAPI
  mapping, no compile-time safety in the dispatcher.
- A single flat `map<string, string> provider_fields` with a `provider_id` discriminator —
  rejected: untyped, can't express required fields per provider, can't evolve per-provider
  schemas independently.
- Separate top-level messages per provider (`PushoverNotification`, `ApnsNotification`) —
  rejected: would violate FR-001's "single canonical message" rule and break the protobuf
  library's stability promise.

---

## 3. OpenAPI generator toolchain

**Decision**: `github.com/oapi-codegen/oapi-codegen/v2` for both server and client.

**Rationale**:

- Generates idiomatic Go (no reflection-heavy runtime), produces one server interface the
  writer implements via generated stubs, and produces one client type the `pkg/`
  library wraps with functional options.
- Supports gorilla/mux as a server target (`-generate gorilla`) — direct match for the
  user's router choice.
- Actively maintained, Go-module-native, no external code generation service required.
- Handles OpenAPI 3 `oneOf` with discriminator cleanly for the canonical Notification
  message.
- Library dependency from generated code is just a small runtime helper package.

**Alternatives considered**:

- `go-swagger` — rejected: OpenAPI 2 native with 3 support bolted on; generator output is
  less idiomatic (package-heavy, struct-ptr-heavy).
- `openapi-generator` (OpenAPITools) — rejected: Java-based generator; larger
  developer-machine dep; generated Go has stylistic friction (e.g., nullable pointers
  everywhere).
- Hand-written HTTP routing — rejected by FR-018.

---

## 4. HTTP request content negotiation (protobuf + JSON)

**Decision**: The OpenAPI operation declares two `requestBody` content types:
`application/x-protobuf` and `application/json`, both pointing at the same
`#/components/schemas/Notification`. The server uses a middleware that decodes into the
protobuf struct based on the `Content-Type` header; the client library accepts a
`WithContentType(...)` option on the submit call.

**Rationale**:

- Both content types describe the *same* canonical message. For JSON, proto3 canonical
  JSON (`protojson.Unmarshal`) is the exact mapping — no custom JSON schema to maintain.
- `oapi-codegen` generates a strongly-typed request struct for the JSON path; for the
  protobuf path we add a 10-line decoder behind the generated handler interface.
- No content negotiation on *responses* — those are always JSON per FR-023 — so the
  asymmetry stays contained to request decoding.

**Implementation note**: oapi-codegen's generated server accepts a `strict-server`
mode that produces a one-operation-per-method interface; the protobuf-decoding path sits
*outside* the generated layer (as a middleware) and converts to the same typed request
before handing off to the generated handler. This keeps the contract (OpenAPI) as the
sole source of truth for both the typed request struct AND the documented content types.

**Alternatives considered**:

- Two separate operations (`POST /notifications.json`, `POST /notifications.protobuf`) —
  rejected: duplicates routing, doubles OpenAPI surface, breaks content-negotiation idiom.
- JSON-only requests — rejected: user explicitly wants protobuf request bodies (FR-002).

---

## 5. Pulsar Go client and native E2EE

**Decision**: `github.com/apache/pulsar-client-go`, version pinned to latest stable at
implementation time. E2EE is configured on the producer via `ProducerOptions.Encryption`
and on the consumer via `ConsumerOptions.Decryption`.

**Rationale**:

- This is the only maintained Go client for Apache Pulsar and supports E2EE natively.
- The E2EE API takes a `CryptoKeyReader` implementation (`pulsar.CryptoKeyReader`
  interface) — the producer asks it for a public key by a caller-chosen key name; the
  consumer asks it for a private key. This maps cleanly onto FR-017's env-referenced key
  file model: our `CryptoKeyReader` implementation reads from a directory supplied by env
  var, indexed by key name.
- Supports RSA private keys only in the default `DefaultMessageCrypto` (parses via
  `x509.ParsePKCS1PrivateKey`). The public key is PKIX-encoded. The spec originally
  considered ECDSA as the default, but integration-testing against the real Pulsar Go
  client revealed the private-key parser is RSA-only. **Default: RSA 2048.** The size
  is config-controlled.
- Multi-key producer encryption is a single slice of key names passed to
  `ProducerOptions.Encryption.Keys`; that directly enables the FR-017 "overlap window"
  rotation mechanic.
- `cryptoFailureAction` on the consumer maps 1:1 to FR-016's hard-reject policy — we set
  it to `pulsar.ConsumerCryptoFailureActionFail` so undecryptable messages do not reach
  application code at all, and our consumer wrapper translates the failure into the
  terminal outcome with reason `encryption-policy-violation`.

**Alternatives considered**:

- Rolling our own E2EE envelope on top of an unencrypted Pulsar topic — rejected: more
  code, duplicates a stable library feature, departs from the clarification decision
  (Pulsar native E2EE).
- Other Pulsar clients — there aren't maintained ones in Go.

---

## 6. Pushover integration approach

**Decision**: A thin, hand-written HTTP wrapper in
`internal/deliverer/provider/pushover/` that issues one `POST
https://api.pushover.net/1/messages.json` per message. No third-party SDK.

**Rationale**:

- Pushover's send API is a single endpoint with ~10 form fields; an SDK would add more
  code than we'd save.
- Constitution dependency hygiene: zero new transitive deps for a 60-line implementation.
- Makes the retry-classification policy explicit (2xx → delivered; 4xx except 429 →
  permanent failure with Pushover's response body as `problem.detail`; 429 and 5xx →
  transient, retryable).
- Keeps Pushover's application API token inside this package; the rest of the codebase
  never sees it.

**Retry/classification specifics** (per FR-009):

| Pushover response | Classification | Terminal status | Reason code |
|-------------------|----------------|-----------------|-------------|
| 2xx with `status: 1` | Success | delivered | — |
| 400 with `errors: [...]` containing "user"/"token" keys | Permanent | permanently-failed | `provider-rejected-recipient` |
| 400 other | Permanent | permanently-failed | `provider-rejected-payload` |
| 401/403 | Permanent (config error — would alert) | permanently-failed | `provider-auth-failed` |
| 429 | Transient | (retry) | — |
| 5xx | Transient | (retry) | — |
| Network error / timeout | Transient | (retry) | — |
| Retry budget exhausted | Terminal | permanently-failed | `retry-budget-exhausted` |

**Alternatives considered**:

- `github.com/gregdel/pushover` — maintained Go SDK. Rejected for dependency hygiene and
  because the constitution's Interface-First principle already requires us to wrap
  whatever we use behind `provider.PushProvider`. The SDK would be a transitive
  dep added for no shape-of-code benefit.

---

## 7. Upstream-service authentication on the writer's HTTP API

**Decision (MVP)**: Bearer tokens in the `Authorization: Bearer <token>` header, validated
against a static set of tokens loaded from a file path referenced by env var
`WRITER_BEARER_TOKENS_FILE`. The file is one token per line with an optional
`<token> <service-name>` pair; the service-name becomes a structured log attribute so
operators can see which caller sent which request.

**Rationale**:

- Spec's FR-004 requires authentication but leaves the mechanism to planning.
- Bearer tokens are the simplest mechanism that meets MVP needs, requires no external
  auth service, aligns with OpenAPI's `securitySchemes: [bearerAuth]` (one-line
  declaration), and is trivially substitutable behind the `auth.Authenticator`
  interface later if the operator wants mTLS or OIDC-JWT.
- Bearer tokens fit the constitution's env-as-configuration model: the token *file* path
  is an env var, and the file is a read-only mount (same lifecycle as the encryption keys).

**Alternatives considered**:

- mTLS — rejected for MVP: requires a PKI story and cert distribution; reconsider once
  multiple upstream services exist.
- OIDC-JWT validation — rejected for MVP: requires an IDP dependency, violating the
  "Pulsar is the only runtime infrastructure dep" constraint the user set.
- HMAC request signing — rejected for MVP: adds caller complexity (each request signed)
  without meaningfully more security than bearer tokens over TLS for this threat model.

The `auth.Authenticator` interface makes all three drop-in replacements later.

---

## 8. Error `type` URI catalogue (for FR-022 / SC-014)

**Decision**: Error `type` URIs use a stable, human-readable, non-dereferenceable URI
scheme rooted at `about:blank` for the default case and
`https://pulsar-notifcation-pipeline.example/problems/<slug>` for categorised errors.
The exact hostname is a config detail; what matters is the **catalogue is authored as
part of the OpenAPI document** so that generated clients and servers share the same set.

Initial catalogue:

| URI slug | HTTP status | Meaning |
|----------|-------------|---------|
| `validation-failed`       | 400 | Request body failed canonical-schema validation (FR-003). Extension: `errors` array with per-field reason codes. |
| `unsupported-content-type`| 415 | `Content-Type` is not `application/json` or `application/x-protobuf`. |
| `authentication-required` | 401 | No `Authorization` header or malformed. |
| `authentication-failed`   | 401 | Bearer token unknown or revoked. |
| `idempotency-conflict`    | 409 | Same idempotency key seen within the window with a divergent payload. |
| `substrate-unavailable`   | 503 | Pulsar producer cannot publish; writer signals caller to retry. Extension: `retry_after_seconds`. |
| `internal`                | 500 | Unexpected server error; safe-to-show detail only. |

**Rationale**:

- Stable URIs let clients do `errors.Is(err, notificationclient.ErrValidationFailed)`
  against the typed error exposed by FR-024's library. The client library exports one
  sentinel error per catalogue entry.
- Keeping the catalogue authored in the OpenAPI document means client-side sentinels
  are generated, not hand-maintained.

**Alternatives considered**:

- `about:blank` for every error (RFC 7807 fallback) — rejected: defeats categorisation;
  SC-014 explicitly requires a URI from a documented catalogue.
- Dereferenceable URIs pointing at public error documentation — deferred: nice to have,
  but the catalogue can be promoted to dereferenceable URIs later without breaking the
  string values.

---

## 9. Enforcing the functional-options pattern (Constitution II)

**Decision**: Add an `analysistest`-compatible custom `go vet` analyzer under
`tools/funcoptslint/` that flags any exported struct constructed via composite literal
outside its own package (i.e., `&someexportedtype{...}` by a consumer). This catches the
common slip of bypassing the options pattern. Ship it as a pre-commit check and a CI job.

**Rationale**:

- The constitution calls this out as non-negotiable. A lint rule is the cheapest way to
  make violations unmergeable.
- Runs in <1s for the whole module.

**Alternatives considered**:

- Reviewer discipline only — rejected: principle is non-negotiable and memory fades.
- `golangci-lint` plugin via `forbidigo` — rejected: `forbidigo` works on identifiers, not
  on syntactic positions. Too blunt.

---

## 10. Testcontainers for Pulsar

**Decision**: `apachepulsar/pulsar:3.3.x` image with the `standalone` command,
orchestrated via `github.com/testcontainers/testcontainers-go`. Each integration test
starts a fresh container with a short startup wait on the `/admin/v2/clusters` endpoint.

**Rationale**:

- Official image, `standalone` mode works offline and in CI.
- testcontainers-go handles image pull, container lifecycle, and port forwarding.
- Tests guarded by `//go:build integration` so the unit test loop stays fast.

**Alternatives considered**:

- Mocking the Pulsar client interface at the `pulsarlib` level only — retained for unit
  tests, but the constitution's Principle IV requires real-dependency integration tests
  for substrate-touching code. Mocks alone are insufficient.

---

## 11. Configuration structure

**Decision**: Two independent config structs, one per binary, parsed via
`caarlos0/env/v11` from environment variables at startup. Shared sub-structs (logging,
tracing, Pulsar connection, encryption key paths) are defined in `internal/config/` and
embedded into the per-binary struct. No global mutable state; config is passed explicitly
into constructors.

**Env-var layout** (each with prefix for its owner binary to avoid collisions):

- Shared: `LOG_LEVEL`, `METRICS_ENABLED`, `METRICS_PORT`, `TRACING_ENABLED`,
  `TRACING_SAMPLERATE`, `TRACING_SERVICE`, `TRACING_VERSION`
- Shared Pulsar: `PULSAR_SERVICE_URL`, `PULSAR_TOPIC`, `PULSAR_SUBSCRIPTION`
- Shared E2EE: `PULSAR_ENCRYPTION_KEY_DIR` (directory of public/private PEM files),
  `PULSAR_ENCRYPTION_KEY_NAMES` (comma-separated list of active key names)
- Writer: `WRITER_HTTP_ADDR`, `WRITER_BEARER_TOKENS_FILE`, `WRITER_IDEMPOTENCY_WINDOW`
- Deliverer: `DELIVERER_CONCURRENCY`, `DELIVERER_RETRY_MAX_ATTEMPTS`,
  `DELIVERER_RETRY_INITIAL_BACKOFF`, `DELIVERER_RETRY_MAX_BACKOFF`,
  `PUSHOVER_APP_TOKEN`, `PUSHOVER_BASE_URL`

**Rationale**:

- Per-binary structs keep each app's surface explicit and make misconfigurations
  (e.g., setting `PUSHOVER_APP_TOKEN` on the writer) a no-op instead of a latent bug.
- Prefixing avoids cross-binary env collisions in `docker-compose.yml`.
- `PULSAR_ENCRYPTION_KEY_NAMES` as a list is how FR-017's overlap window is expressed:
  during rotation the writer is configured with both the outgoing and the incoming key
  names.

**Alternatives considered**:

- A single shared config struct — rejected: each binary would carry irrelevant config,
  violating the "explicit surface" intent.
- Config files (YAML/TOML) — rejected by Constitution V (env-based only).

---

## 12. Forward/backward compatibility verification for the protobuf library (SC-007)

**Decision**: A `notification_compat_test.go` integration test that vendors a frozen
copy of the previous minor version's generated Go (under
`testdata/notificationpb_prev/`), then marshals with one and unmarshals with the other in
both directions. Run on every CI build. The test fails if a new schema change is not
wire-compatible; the fix is either to make the change compatible or to bump the major
version of the protobuf library.

**Rationale**:

- Empirical test of FR-013, not just a lint.
- Requires `testdata/` maintenance: after each intentional breaking change (rare, and a
  major-version event), the frozen copy is rotated. Procedure lives in quickstart.md.

---

## 13. Documentation and dev loop

**Decision**: `make` targets for `generate` (proto + openapi), `test`, `test-integration`,
`lint`, `build`, `run-writer`, `run-deliverer`, and `up` (docker-compose). The existing
Grafana LGTM docker-compose scaffold is extended with a `pulsar` service.

**Rationale**: One-command regeneration after contract edits and one-command local E2E
are the right feedback loops for a pipeline with generated artefacts.

---

## Open items carried forward (non-blocking for Phase 1)

1. **Algorithm choice for Pulsar E2EE keys**: ECDSA P-256 recommended as default; RSA
   2048+ supported. Final call is a deployment-time decision, not a code decision.
2. **Error-type URI hostname**: the catalogue uses `pulsar-notifcation-pipeline.example`
   as a placeholder; real hostname decided when the project is deployed.
3. **Pushover application API token provisioning**: operational responsibility; no code
   change needed.

None of the above blocks Phase 1 design.
