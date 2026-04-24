# Feature Specification: End-to-End Push Notification Pipeline

**Feature Branch**: `001-notification-pipeline`
**Created**: 2026-04-23
**Status**: Draft
**Input**: User description: "Build a set of applications (including protocol buffer library for the standard message) to read message data from an API endpoint, travel through Apache Pulsar, and out to the writer which will be responsible for push notification delivery"

## Clarifications

### Session 2026-04-23

- Q: Which wire-encryption mechanism should the pipeline use? → A: Pulsar native end-to-end encryption. The writer's Pulsar client encrypts the message body with the configured public key; the reader/deliverer's client decrypts with the private key. Brokers only see ciphertext. The canonical protobuf schema is unchanged by this choice.
- Q: What, if anything, stays visible to parties with Pulsar access (broker operators, topic inspectors)? → A: Only the pipeline-assigned notification ID, carried as a plaintext Pulsar message property. Every other field (user key, title, body, idempotency key, timestamps) lives inside the encrypted body.
- Q: How are the public and private keys provisioned and rotated? → A: Each key is mounted as a file; its path is supplied via an environment variable. Rotation is a documented operational procedure in which the writer is configured with both the outgoing and incoming public keys for an overlap window, so messages encrypted under the old key can be drained by consumers still holding the old private key.
- Q: What should the reader/deliverer do with a message that is unencrypted or that it cannot decrypt? → A: Hard reject. The message is marked as terminally failed with reason `encryption-policy-violation`, emits an alert-worthy metric, is not retried, and is not dispatched to Pushover.
- Q: Does the spec include an importable HTTP client library for upstream services? → A: Yes, with additional shape. The writer's HTTP API is specified by an OpenAPI document that is the source of truth; both the writer's HTTP handler stubs and an importable Go HTTP client library (published under `pkg/`) are generated from it so the contract cannot drift. The writer's HTTP API accepts request bodies in both protobuf and JSON (proto3 canonical JSON mapping). A separate shared Pulsar communication library (module-internal) is used by both the writer and the reader/deliverer.
- Q: What shape do successful HTTP responses from the writer take? → A: A wrapped JSON envelope of the form `{ "data": <endpoint-specific payload>, "meta": { "request_id": "<server-generated>" } }`. For the notification-submission endpoint, `data` is `{ "notification_id": "<pipeline-assigned identifier>", "accepted_at": "<ISO-8601 UTC>" }` and the HTTP status is `202 Accepted`. The client library (FR-019) MUST expose typed convenience accessors so callers can read fields without walking the envelope by hand.
- Q: Do response bodies ever use protobuf, or are they always JSON? → A: Always JSON, regardless of request content type. Success responses are `application/json` using the `{data, meta}` envelope; error responses are `application/problem+json` per RFC 9457 and are produced server-side via the `github.com/alpineworks/rfc9457` library. The writer's OpenAPI contract has one success-response schema and one error-response schema; response content type is not content-negotiated.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Upstream service submits a notification and the target user receives a push (Priority: P1)

An upstream service (for example, a product backend that wants to notify a user about an event) submits a notification to the pipeline's inbound API. The notification crosses the pipeline and results in a push notification arriving on the intended end-user's Pushover-enrolled device(s).

**Why this priority**: This is the entire reason the system exists. Without this end-to-end path, no other capability matters. It is the MVP.

**Independent Test**: Submit a valid notification to the inbound API naming a Pushover user key that belongs to a test account, and confirm the notification arrives on the Pushover client logged into that account. No other user story needs to exist for this to be demonstrable.

**Acceptance Scenarios**:

1. **Given** an upstream service with valid credentials and a valid canonical notification payload referencing a known Pushover user key, **When** it submits the notification to the inbound API, **Then** the API returns a success acknowledgement within the documented latency budget and the payload is published to the messaging substrate.
2. **Given** a notification published to the messaging substrate, **When** the reader/deliverer consumes it, **Then** the reader/deliverer dispatches it to Pushover using the configured application credentials and the user key carried in the message.
3. **Given** a successful dispatch to Pushover, **When** Pushover accepts the request, **Then** the pipeline records a terminal outcome of "delivered" against that notification's identifier.

---

### User Story 2 — Resilience to transient failures (Priority: P2)

When Pushover or the messaging substrate is momentarily unavailable or returns a retryable error, the pipeline retries delivery without losing messages and without operator intervention. Permanent failures (for example, an invalid user key) are recorded as terminal outcomes and do not consume retry budget forever.

**Why this priority**: Users eventually hit this path in production, but the happy path (US1) must exist first for resilience to matter. P2 reflects "required before a production launch but not required to demonstrate the pipeline".

**Independent Test**: Simulate Pushover returning a retryable error for a bounded interval (e.g., via a stub that returns HTTP 5xx), submit notifications during that interval, restore the stub to success, and verify that (a) no notification was silently dropped and (b) retries stopped promptly once successful.

**Acceptance Scenarios**:

1. **Given** Pushover returning a transient error, **When** the reader/deliverer attempts to dispatch a notification, **Then** the pipeline retries with backoff up to a configured retry budget and records each attempt.
2. **Given** Pushover returning a permanent error (for example, "user key invalid"), **When** the reader/deliverer attempts to dispatch a notification, **Then** the pipeline marks the notification as terminally failed with a clear reason code and does not retry.
3. **Given** the messaging substrate being momentarily unavailable to the writer, **When** upstream services continue submitting notifications, **Then** the inbound API either rejects submissions with a retry-signalling response or buffers them in a bounded, observable way — it does not silently succeed.

---

### Edge Cases

- The inbound payload does not conform to the canonical schema (missing required fields, wrong types).
- The inbound API is called without valid authentication credentials.
- The same notification is submitted twice with the same idempotency identifier within a short window.
- A notification payload exceeds Pushover's documented size or rate limits.
- The reader/deliverer restarts mid-batch; unacknowledged messages must be redelivered on restart rather than lost.
- Pushover returns an ambiguous response (for example, a 5xx with no body) — must be treated as retryable, not as success.
- An upstream service submits notifications faster than downstream can deliver (backpressure).
- A notification carries an expiry timestamp that has already passed by the time it is consumed — the reader/deliverer must drop it with a terminal "expired" outcome rather than deliver a stale alert.
- A message arrives on the topic that is unencrypted or encrypted under a key the reader/deliverer cannot decrypt — must be terminally failed with reason `encryption-policy-violation`, never dispatched to Pushover, and must surface as an alert-worthy operational signal.
- During a key rotation overlap window, in-flight messages encrypted under the outgoing key continue to be decryptable by consumers still holding the outgoing private key; rotation completes only once the outgoing key has been fully drained.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The pipeline MUST expose a single canonical notification message schema as a reusable protocol buffer library that is the sole wire contract between the writer, the messaging substrate, and the reader/deliverer.
- **FR-002**: The writer MUST expose an HTTP API endpoint that upstream services POST notifications to (server model — the writer does not poll an external API). The endpoint MUST accept request bodies encoded as either protobuf (content type `application/x-protobuf` or `application/protobuf`) or JSON conforming to the proto3 canonical JSON mapping of the canonical message (content type `application/json`), using standard HTTP content negotiation.
- **FR-003**: The writer MUST validate every submission against the canonical schema and reject non-conforming submissions with a correlatable error response.
- **FR-004**: The writer MUST authenticate submissions from upstream services and reject unauthenticated requests.
- **FR-005**: The writer MUST publish each accepted notification to the messaging substrate exactly once per successful acknowledgement to the upstream caller.
- **FR-006**: The reader/deliverer MUST consume notifications from the messaging substrate and dispatch each via the configured push provider.
- **FR-007**: The first-release push provider is Pushover. The reader/deliverer MUST sit behind a push-provider interface (per the project constitution's Interface-First principle) so that additional providers can be added in later features without changes to the pipeline core.
- **FR-008**: The canonical notification payload MUST carry the target Pushover user or group key (and optionally a specific device name) directly. The pipeline MUST NOT maintain an internal device-registration store in the MVP; recipient identity is supplied by the caller.
- **FR-009**: The reader/deliverer MUST distinguish transient provider errors (retry) from permanent ones (terminal failure) and MUST retry transient failures with bounded backoff.
- **FR-010**: The pipeline MUST assign each notification a stable identifier on intake and MUST carry that identifier through every stage so a single notification can be correlated end-to-end in logs and metrics.
- **FR-011**: The pipeline MUST record, for every notification, a terminal delivery outcome (delivered, permanently failed with reason, or expired) observable by operators.
- **FR-012**: Both applications MUST expose health and liveness signals suitable for automated orchestration (container schedulers, load balancers).
- **FR-013**: The canonical protobuf library MUST be versioned; a consumer built against a prior minor version MUST continue to accept messages produced by a newer minor version, and a consumer built against a newer minor version MUST continue to accept messages produced by a prior minor version (forward and backward wire compatibility within a major version).
- **FR-014**: Upstream services MUST be able to submit notifications with an optional client-supplied idempotency key that, when present and repeated within a configured window, results in the pipeline dispatching the notification at most once.
- **FR-015**: The writer MUST publish every notification to Pulsar using Pulsar's native end-to-end encryption with a configured public key; the message body (carrying all canonical fields except the notification identifier) MUST be ciphertext from the moment it leaves the writer process. The pipeline-assigned notification identifier MUST be set as a plaintext Pulsar message property to support operational correlation. No other canonical field MAY appear in plaintext outside the writer or reader/deliverer processes.
- **FR-016**: The reader/deliverer MUST decrypt every consumed message using its configured private key. If a message is unencrypted, or its ciphertext cannot be decrypted with any currently configured private key, the reader/deliverer MUST record a terminal outcome with reason `encryption-policy-violation`, MUST emit an alert-worthy operational metric, MUST NOT retry, and MUST NOT dispatch the message to Pushover.
- **FR-017**: The writer's public key(s) and the reader/deliverer's private key(s) MUST be loaded at startup from filesystem paths referenced by environment variables. The writer MUST support being configured with more than one active public key simultaneously so that, during a key rotation overlap window, it can encrypt each outgoing message for all currently-valid consumer keys, allowing in-flight messages encrypted under an outgoing key to be drained by consumers still holding the corresponding private key.
- **FR-018**: The writer's HTTP API MUST be described by an OpenAPI document checked into this repository that is the authoritative contract for the API surface (paths, content types, request/response schemas, error shapes, auth header scheme). The writer's HTTP handler stubs and the importable Go HTTP client library (FR-019) MUST both be generated from this single OpenAPI document; hand-written HTTP routing or request-construction code that duplicates the OpenAPI surface is prohibited.
- **FR-019**: The module MUST publish an importable Go HTTP client library at `pkg/<client-package-name>/` so that any other Go application can import it to submit notifications to the writer without hand-rolling HTTP calls. The client library MUST be generated from the same OpenAPI document as the server stubs (FR-018), MUST expose protobuf-typed request construction, MUST allow the caller to choose `application/x-protobuf` or `application/json` as the request content type per FR-002, and MUST surface auth header attachment and the idempotency key (FR-014) as first-class API concerns.
- **FR-020**: Both the writer and the reader/deliverer MUST talk to Apache Pulsar through a shared, module-internal Pulsar communication library (e.g., `internal/pulsar/` or equivalent). This library MUST encapsulate producer/consumer construction, encryption configuration per FR-015 and FR-016, and connection/config lifecycle concerns — so that substrate-specific code is not duplicated between the two applications and can be substituted behind a Go interface per the project constitution's Interface-First principle.
- **FR-021**: The writer's HTTP API MUST return every successful response in a standard JSON envelope of the form `{ "data": <endpoint-specific payload>, "meta": { "request_id": "<server-generated request ID>" } }`, with content type `application/json`. For the notification-submission endpoint, `data` MUST be `{ "notification_id": "<pipeline-assigned identifier>", "accepted_at": "<ISO-8601 UTC timestamp>" }` and the HTTP status code MUST be `202 Accepted`. The `meta.request_id` MUST be a server-generated correlation identifier for the HTTP request distinct from the notification identifier, and MUST be emitted on every response (success or error) to support request-scoped operator correlation.
- **FR-022**: The writer's HTTP API MUST return every error response in the RFC 9457 "Problem Details for HTTP APIs" format with content type `application/problem+json`. Each problem document MUST populate `type` (a stable URI identifying the error category, not a free-text string), `title`, `status`, `detail`, and `instance`, and MAY include extension members that categorise the error for machine consumption (for example, per-field validation reasons for FR-003 failures, and auth-failure-mode codes for FR-004 rejections). The writer's server implementation MUST use the `github.com/alpineworks/rfc9457` library so that error shape and header handling remain consistent across all endpoints.
- **FR-023**: Response bodies MUST always be JSON regardless of the request's content type (see FR-002). Success responses MUST use `application/json`; error responses MUST use `application/problem+json`. Protobuf MUST NOT be used for response bodies. The OpenAPI contract MUST define exactly one success-response schema shape (the envelope of FR-021) and exactly one error-response schema (the RFC 9457 problem document of FR-022).
- **FR-024**: The generated Go HTTP client library (FR-019) MUST expose typed convenience accessors so that callers read response fields without walking the envelope by hand. At minimum: on success, the response type MUST expose strongly-typed accessors for the endpoint-specific `data` fields (e.g., `NotificationID()`, `AcceptedAt()`) and for `meta.request_id`; on error, the library MUST parse the RFC 9457 problem document into a typed Go error that carries the `type` URI, `status`, `detail`, `instance`, any extension members, and the request ID, and that satisfies the standard `error` interface so callers can use `errors.Is` / `errors.As` against sentinel error categories exposed by the library.

### Key Entities *(include if feature involves data)*

- **Notification**: The canonical message carried through the pipeline. Attributes: a pipeline-assigned identifier, an optional client idempotency key, recipient targeting (Pushover user/group key; optional device name), payload (title, body, priority, optional supplementary fields supported by Pushover such as URL and sound), lifecycle timestamps (created, expires), and the originating service's identifier.
- **Delivery Outcome**: The record of what happened to a notification. Attributes: the notification identifier, terminal status (delivered / permanently-failed / expired), a categorised reason code (including `encryption-policy-violation` for messages that fail the FR-016 policy) and optional provider response, the number of delivery attempts, and the timestamps of first and last attempt.
- **Push Provider**: A logical endpoint the pipeline dispatches to. At MVP there is one implementation: Pushover. Attributes: a provider identifier, capability flags (payload size limit, priority support), and credentials (opaque to the pipeline core; managed via configuration).
- **Encryption Key Material**: The public key(s) configured on the writer and the private key(s) configured on the reader/deliverer. Attributes: the filesystem path each key is loaded from (supplied via environment variable), the set of keys currently active, and — on the writer — the subset of public keys to which each outgoing message is encrypted (all currently active public keys during a rotation overlap window).
- **HTTP API Contract (OpenAPI document)**: The authoritative description of the writer's HTTP API. Attributes: path and operation definitions, accepted request content types (`application/x-protobuf`, `application/json`), request schemas (referencing the canonical message via its proto3 canonical JSON mapping), response schemas — exactly one success envelope schema (`application/json` with `{data, meta}` per FR-021) and exactly one error schema (`application/problem+json` per RFC 9457 / FR-022), auth header scheme, the catalogue of stable error `type` URIs with their human titles, and a contract version. Consumed by a code generator to produce the writer's server stubs and the `pkg/` client library.
- **Client Library (`pkg/`)**: An importable Go package, generated from the OpenAPI document, that upstream Go services use to submit notifications without hand-rolling HTTP calls. Versioned with the module. Surface includes protobuf-typed request construction, content-type selection, auth header attachment, and idempotency-key plumbing.
- **Pulsar Communication Library (`internal/`)**: A module-internal Go package used by both the writer and the reader/deliverer to talk to Pulsar. Encapsulates producer/consumer construction, E2EE configuration (FR-015, FR-016), and connection/config lifecycle. Accessed through a Go interface so the substrate remains substitutable for tests.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a valid, well-formed notification submitted during healthy operation, the upstream service receives an acknowledgement from the writer within 250 ms at the 95th percentile.
- **SC-002**: For a valid notification, the end-to-end time from writer acknowledgement to Pushover accepting the dispatch is under 2 seconds at the 95th percentile under nominal load.
- **SC-003**: Under nominal load, at least 99.9% of submitted notifications reach a terminal outcome of "delivered" or a terminal outcome with an explicit, categorised reason — not stuck, not silently dropped.
- **SC-004**: When Pushover is unavailable for up to 5 minutes, zero notifications submitted during that window are lost; all are either delivered after recovery or recorded with a terminal reason.
- **SC-005**: An operator given only a pipeline-assigned notification identifier can locate the corresponding log and metric records at every stage of the pipeline (writer receipt, substrate publish, substrate consume, provider dispatch, terminal outcome) without cross-referencing external systems.
- **SC-006**: The pipeline sustains a submission rate of at least 500 notifications per second per instance of each application under nominal load without falling behind (no growing backlog on the messaging substrate).
- **SC-007**: A protobuf consumer built against version N of the message library successfully parses a message produced by version N+1 of the library (same major version), and vice versa, without code changes.
- **SC-008**: Adding a second push provider in a future feature requires no changes to the writer, the messaging substrate, or the canonical protobuf schema — only a new implementation of the push-provider interface inside the reader/deliverer.
- **SC-009**: A party with read access to Pulsar topics (broker operator, topic inspector, anyone able to read raw broker-side message data) observes only ciphertext message bodies and, as plaintext metadata, only the pipeline-assigned notification identifier. No user key, title, body, idempotency key, or timestamp from a notification appears in plaintext outside the writer and reader/deliverer processes.
- **SC-010**: An unencrypted message or a message encrypted under a key that no configured private key can decrypt results in a terminal `encryption-policy-violation` outcome recorded within the normal outcome-recording latency budget, emits a metric suitable for alerting, and produces zero dispatches to Pushover for that message.
- **SC-011**: The writer's HTTP server stubs and the importable Go client library in `pkg/` are regenerated from the repository's OpenAPI document in a single build step, producing zero manual diffs in generated files; a drift between the OpenAPI document and either generated artifact is detectable by CI and fails the build.
- **SC-012**: An upstream Go service can successfully submit a valid notification to the writer in three lines or fewer of call-site code using the published client library, and receive either a typed success response or a typed categorised error — without importing any HTTP library directly and without writing any URL, header, or marshaling code by hand.
- **SC-013**: Every success response from the writer conforms to the `{data, meta}` envelope (FR-021): content type is `application/json`, the `data` and `meta` members are present, and `meta.request_id` is a non-empty string. This is verifiable by an OpenAPI-schema-driven response validator in CI with zero violations across all endpoints.
- **SC-014**: Every error response from the writer conforms to RFC 9457 (FR-022): content type is `application/problem+json`, `type` is a URI from the contract's documented catalogue (never a free-text string), and `status` / `title` / `detail` / `instance` are all populated. Verifiable by schema-driven validation in CI with zero violations across all documented error categories.

## Assumptions

- Terminology follows the project constitution: **writer** = API → Pulsar (accepts submissions and publishes), **reader/deliverer** = Pulsar → Pushover (consumes and delivers).
- The canonical message carried through the pipeline is a single protobuf schema; there is no per-upstream-service custom schema. Upstream services adapt to the canonical schema.
- "Push notification delivery" for the MVP means delivery via the Pushover service (https://pushover.net). Delivery to APNs, FCM, web push, SMS, or email is out of scope for this feature.
- Delivery semantics are **at-least-once** end-to-end. Pushover itself deduplicates nothing on our behalf; duplicate deliveries within a narrow window may produce duplicate user-visible notifications and are accepted as a trade-off against implementation complexity. Upstream services wanting stronger guarantees use the optional idempotency key (FR-014).
- The recipient's Pushover user/group key is supplied by the caller in each notification payload; the pipeline does not maintain a mapping from internal user IDs to Pushover keys in the MVP. Upstream services own that mapping.
- The Pushover application credentials (application API token) are configured on the reader/deliverer via environment variables, per the project constitution's configuration principle.
- Operators will run one instance (or a small pool) of each application per environment; massive horizontal scale (multi-region, hundreds of instances) is not an initial requirement.
- The messaging substrate is Apache Pulsar (per the project constitution); substrate-specific behaviour is accessed behind an interface so that substrate substitution remains possible for tests and future evolution.
- Authentication of upstream services calling the writer's API uses a conventional mechanism (e.g., bearer tokens or mTLS) selected at planning time; the exact mechanism is a technical detail, not a scope choice.
- Rate limiting and quota enforcement per upstream service are not in the MVP; backpressure is handled by the substrate and by documented writer behaviour under overload. Pushover's own rate limits are respected by the reader/deliverer's retry/backoff behaviour.
- Scheduled / delayed delivery (send-at-a-future-time) is out of scope for the initial cut.
- Observability data (logs, metrics, traces) flows into the environment's existing observability stack, per the project constitution (structured logging via slog, OpenTelemetry for metrics and traces).
- End-to-end payload encryption uses Pulsar's native client-side encryption feature; the specific asymmetric algorithm (e.g., ECIES, RSA-OAEP) and key format are planning-phase details bounded by what the Pulsar Go client supports. Transport-level TLS to Pulsar is additive (recommended at deploy time) and orthogonal to the E2EE requirement.
- Key rotation is operator-driven; there is no automated rotation service in scope. Operators are responsible for declaring an overlap window, configuring the writer with both outgoing and incoming public keys during that window, and retiring the outgoing key once the topic backlog encrypted under it has been fully drained.
- The canonical notification message is defined exactly once in the protobuf schema (FR-001). The JSON accepted by the writer's HTTP API (FR-002) is the proto3 canonical JSON mapping of that same message; there is no separate hand-designed JSON schema to maintain.
- The specific OpenAPI generator toolchain (e.g., `oapi-codegen` vs. `openapi-generator` vs. `go-swagger`) is a planning-phase selection. Scope requires only that a single tool generate both the writer's HTTP server stubs and the `pkg/` client library from one OpenAPI document, with drift caught in CI.
- The shared Pulsar communication library (FR-020) is module-internal (lives under `internal/`) because only this repository's two applications need it. Externalising it to `pkg/` would be a later, separate decision if and when a third application needs to talk to the same Pulsar setup.
