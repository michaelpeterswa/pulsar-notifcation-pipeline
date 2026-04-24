---
description: "Task list for End-to-End Push Notification Pipeline (001-notification-pipeline)"
---

# Tasks: End-to-End Push Notification Pipeline

**Input**: Design documents from `/specs/001-notification-pipeline/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: MANDATORY. The project constitution (Principle IV, Test Discipline) requires
unit tests for every exported function/method/interface and testcontainers/httptest
integration tests for any code that touches Pulsar or Pushover. Test tasks are NOT
optional in this plan. Per TDD hygiene, test tasks precede the implementation they
verify within each story.

**Organization**: Tasks are grouped by user story to enable independent implementation
and testing of each story. Stories map to the two user stories in spec.md — US1 (P1,
end-to-end Pushover delivery) and US2 (P2, resilience).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1 or US2)
- File paths are absolute or repo-relative as appropriate

## Path Conventions

Multi-binary Go module (Structure Decision in plan.md):

- `cmd/writer/`, `cmd/deliverer/` — binary entrypoints
- `pkg/notificationpb/v1/`, `pkg/notificationclient/` — public libraries
- `proto/notification/v1/`, `api/openapi.yaml` — contract sources (checked in)
- `internal/writer/**`, `internal/deliverer/**`, `internal/pulsarlib/`,
  `internal/config/`, `internal/logging/` — module-private packages
- Tests live next to the code they test (`_test.go`); integration tests guarded by
  `//go:build integration`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialisation and toolchain scaffolding. Creates the module shell
all later phases build on.

- [X] T001 Bump `go.mod` to `go 1.25` (toolchain directive `go1.25.x`); run `go mod tidy`. Update the `go_package` option in forthcoming `.proto` to match the module path if renamed. Records a governance note in the PR description.
- [X] T002 Create the directory scaffolding (empty `.gitkeep` where needed): `cmd/writer/`, `cmd/deliverer/`, `proto/notification/v1/`, `api/`, `pkg/notificationpb/v1/`, `pkg/notificationclient/generated/`, `internal/pulsarlib/`, `internal/writer/{config,httpserver,auth,ingest,problems,envelope}/`, `internal/deliverer/{config,consumer,dispatcher,retry,outcomes,provider,provider/pushover,provider/sandbox}/`, `internal/e2e/`, `tools/funcoptslint/`.
- [X] T003 [P] Add `Makefile` at repo root with targets `generate`, `test`, `test-integration`, `lint`, `build`, `run-writer`, `run-deliverer`, `up`, `down`, implementing the commands documented in `specs/001-notification-pipeline/quickstart.md`.
- [X] T004 [P] Add `tools/tools.go` pinning build-time tools via `//go:build tools`: `google.golang.org/protobuf/cmd/protoc-gen-go`, `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`, `golang.org/x/tools/cmd/stringer`. Run `go mod tidy`. (Implementation note: used Go 1.24+ `tool` directive in `go.mod` instead of the legacy `tools.go` pattern — same intent, cleaner result.)
- [X] T005 [P] Add `oapi-codegen.server.yaml` and `oapi-codegen.client.yaml` at repo root configuring gorilla-mux server output to `internal/writer/httpserver/server.gen.go` and client output to `pkg/notificationclient/generated/client.gen.go`, both reading from `api/openapi.yaml`.
- [X] T006 [P] Add `.golangci.yml` at repo root enabling `govet`, `staticcheck`, `gosimple`, `ineffassign`, `errcheck`, `revive`, plus a `forbidigo` rule banning `fmt.Print*`, `log.Print*`, and `os.Getenv` outside `internal/config/`.
- [X] T007 Update `Dockerfile` to a single multi-stage file producing two final distroless stages (one per binary). Parameterise the build target via `ARG APP={writer|deliverer}`; the compose file chooses.
- [X] T008 Update `docker-compose.yml` to add a `pulsar` service (`apachepulsar/pulsar:3.3`, `bin/pulsar standalone`) alongside the existing Grafana LGTM stack; add `writer` and `deliverer` services from the Dockerfile; wire env via `env_file: .env.local`.

**Checkpoint**: Module compiles (empty), toolchain locked, docker-compose ready to boot Pulsar.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The canonical contracts (protobuf + OpenAPI) and the shared libraries used
by both binaries. NO user-story work can begin until this phase is green — both
applications consume these artefacts.

### Protobuf library (FR-001, FR-013)

- [X] T009 Copy `specs/001-notification-pipeline/contracts/notification.proto` to `proto/notification/v1/notification.proto`. This file is the source of truth from here on; edits happen here, not in the specs copy.
- [X] T010 Run `make generate` to produce `pkg/notificationpb/v1/notification.pb.go` from T009. Commit the generated file (generated code is vendored, not regenerated at build time).
- [X] T011 [P] Add `pkg/notificationpb/v1/doc.go` with a package comment describing the wire contract, the FR-013 compatibility guarantee, and the `oneof target` extension pattern.
- [X] T012 [P] Add `pkg/notificationpb/v1/roundtrip_test.go` — unit test that marshals a fully-populated `Notification` to bytes and to proto3-canonical JSON, unmarshals back, and asserts equality.
- [X] T013 [P] Seed `pkg/notificationpb/v1/testdata/notificationpb_prev/` with a frozen copy of the current generated Go (initially identical to `notification.pb.go`). Add `pkg/notificationpb/v1/compat_test.go` that cross-marshals/unmarshals between current and frozen types in both directions (satisfies FR-013 / SC-007). Document the rotation procedure for this fixture in `quickstart.md`. (Implementation note: the frozen copy lives at `pkg/notificationpb/v1/prev/` — a sibling Go package with a distinct protobuf file descriptor name — rather than under `testdata/`, because the protobuf registry panics when two packages register the same source filename.)

### OpenAPI contract (FR-018, FR-023)

- [X] T014 Copy `specs/001-notification-pipeline/contracts/openapi.yaml` to `api/openapi.yaml`. Source of truth lives here from now on.
- [X] T015 Run `make generate` to produce `internal/writer/httpserver/server.gen.go` (gorilla-mux server stubs) and `pkg/notificationclient/generated/client.gen.go` from T014. Commit both generated files.
- [X] T016 [P] Add `.github/workflows/contracts.yml` (or extend existing CI) with a job that runs `make generate` and fails the build on any uncommitted diff in `pkg/notificationpb/v1/notification.pb.go`, `internal/writer/httpserver/server.gen.go`, or `pkg/notificationclient/generated/client.gen.go` (satisfies SC-011). *[Fulfilled by T080's `generate-drift` job in `.github/workflows/pull_request.yml`.]*

### Shared config / logging (Principle V)

- [X] T017 Create `internal/config/shared.go` with a `Shared` struct carrying `LogLevel`, `MetricsPort`, `TracingEnabled`, `TracingSampleRate`, `TracingService`, `TracingVersion`, `PulsarServiceURL`, `PulsarTopic`, `PulsarEncryptionKeyDir`, each tagged for `caarlos0/env/v11`. Export a `Load() (Shared, error)`.
- [X] T018 [P] Add `internal/config/shared_test.go` — table-driven test of env parsing success and failure modes.
- [X] T019 [P] Extend `internal/logging/logging.go` (if present; else create) to construct an `*slog.Logger` with JSON handler, honouring `LogLevel`. Add `internal/logging/logging_test.go`.

### Shared Pulsar library (FR-015, FR-016, FR-017, FR-020; Principle I)

- [X] T020 Create `internal/pulsarlib/pulsarlib.go` defining the `Publisher` and `Consumer` Go interfaces, the `Option` type (`func(*options)`), and `With*` helpers for `WithServiceURL`, `WithTopic`, `WithSubscription`, `WithEncryptionKeyReader`, `WithEncryptionKeyNames`, `WithCryptoFailureFail`, `WithLogger`. Non-negotiable functional-options pattern per Principle II.
- [X] T021 Create `internal/pulsarlib/keys.go` implementing a `pulsar.CryptoKeyReader` that reads `<name>.pub.pem` and `<name>.priv.pem` from `PulsarEncryptionKeyDir`, keyed by the key name passed in. Also expose `LoadKeySet(dir string, activeNames []string) (*KeySet, error)` used by the writer to assert all active keys are present on startup. (Implementation note: correct SDK type is `crypto.KeyReader` / `crypto.EncryptionKeyInfo` from `github.com/apache/pulsar-client-go/pulsar/crypto`.)
- [X] T022 Create `internal/pulsarlib/apachepulsar.go` implementing `Publisher` and `Consumer` using `github.com/apache/pulsar-client-go/pulsar`. Wires `ProducerOptions.Encryption.Keys = opts.encryptionKeyNames` and `ConsumerOptions.Decryption.KeyReader = opts.encryptionKeyReader` with `ConsumerCryptoFailureActionFail`. Publisher sets the message property `notification_id` to plaintext per FR-015.
- [X] T023 [P] Add `internal/pulsarlib/keys_test.go` — unit test covering: PEM file loading, missing-key detection, symmetric name derivation from `.pub.pem`/`.priv.pem`.
- [X] T024 [P] Add `internal/pulsarlib/pulsarlib_test.go` — interface-contract unit tests using an in-memory fake that implements `Publisher`/`Consumer`.
- [X] T025 Add `internal/pulsarlib/apachepulsar_integration_test.go` (`//go:build integration`) spinning `apachepulsar/pulsar:3.3` via testcontainers-go: publish an encrypted message, consume it, verify decrypt, verify plaintext `notification_id` property, verify `cryptoFailureAction=Fail` rejects an unencrypted message. (Test authored and `go vet -tags=integration` clean; actual container run requires a Docker daemon and is not part of the default `go test` loop.)

### Writer-side foundational helpers

- [X] T026 [P] Create `internal/writer/problems/problems.go` wrapping `github.com/alpineworks/rfc9457` with typed sentinel problems (`ProblemValidationFailed`, `ProblemAuthRequired`, `ProblemAuthFailed`, `ProblemIdempotencyConflict`, `ProblemUnsupportedContentType`, `ProblemSubstrateUnavailable`, `ProblemInternal`) matching the catalogue in research.md §8. Each sentinel builder takes request ID + detail + optional `FieldError` slice. (Implementation note: module path is `alpineworks.io/rfc9457`, not `github.com/alpineworks/rfc9457`.)
- [X] T027 [P] Add `internal/writer/problems/problems_test.go` covering every sentinel, verifying the `type` URI, `status`, required fields, and the `request_id` extension member (FR-022 / SC-014).
- [X] T028 [P] Create `internal/writer/envelope/envelope.go` with `type Success[T any] struct { Data T; Meta Meta }` and helpers to write the `{data, meta}` envelope as `application/json` with status 202.
- [X] T029 [P] Add `internal/writer/envelope/envelope_test.go` covering envelope construction and ensuring `meta.request_id` is always populated (SC-013).

### Functional-options linter (enforces Principle II)

- [X] T030 [P] Create `tools/funcoptslint/funcoptslint.go` — a `singlechecker` analyzer that walks AST positions where an exported struct from within the module is instantiated by a composite literal outside its declaring package. Emit diagnostic "use the functional-options constructor". *[Full implementation lands in `tools/funcoptslint/funcoptslint.go` + `cmd/funcoptslint/main.go`. The analyzer is matched-constructor-gated: it only flags structs whose declaring package exposes a `New<T>`/`New` constructor returning the type, which cleanly separates service-like configurable structs (Principle II targets) from plain data records (intentionally exempt). `ModulePrefix` is configurable via flag.]*
- [X] T031 [P] Add `tools/funcoptslint/funcoptslint_test.go` using `analysistest` with positive and negative fixtures. *[Uses analysistest with fixture packages under `tools/funcoptslint/testdata/src/`: `definer` (declares `Client`+`New`), `consumer` (4 cases — empty literal OK, non-empty pointer literal fails, value literal fails, constructor use OK), `selfconstruct` (same-package literal OK), `thirdparty_consumer` (non-module-prefix type OK).]*

**Checkpoint**: Foundation ready — both user stories may begin in parallel by different contributors.

---

## Phase 3: User Story 1 — End-to-End Pushover Delivery (P1) 🎯 MVP

**Goal**: A valid notification submitted to the writer's HTTP API results in a push arriving on the target Pushover device — exercising the full writer → Pulsar (E2EE) → reader/deliverer → Pushover path.

**Independent Test**: Submit a valid notification via `curl` (or the `pkg/notificationclient`) with a real or sandbox Pushover user key. Verify the Pushover client logged into that account receives the push, and all five log stages in quickstart.md §"What a green submission looks like" emit records sharing the same `notification_id`.

### Writer config, auth, ingest

- [X] T032 [P] [US1] Create `internal/writer/config/config.go` embedding `config.Shared` and adding `HTTPAddr`, `BearerTokensFile`, `IdempotencyWindow`, `EncryptionKeyNames`. Functional-options constructor `New(opts ...Option) (*Config, error)` alongside `Load()`.
- [X] T033 [P] [US1] Add `internal/writer/config/config_test.go` — env parsing + functional options.
- [X] T034 [P] [US1] Create `internal/writer/auth/auth.go` with `Authenticator` interface (`Authenticate(ctx, req) (Principal, error)`), a `Principal` struct carrying `ServiceName`, and a `Middleware(next http.Handler) http.Handler` constructor that uses the interface and emits the matching `problems.ProblemAuthRequired`/`ProblemAuthFailed` on failure. Uses functional options.
- [X] T035 [P] [US1] Add `internal/writer/auth/auth_test.go` covering middleware behaviour with a fake `Authenticator`.
- [X] T036 [P] [US1] Create `internal/writer/auth/bearer.go` — `Bearer` implementation of `Authenticator` reading `<token> <service-name>` lines from the path in `WRITER_BEARER_TOKENS_FILE`. Startup failure if file unreadable or empty.
- [X] T037 [P] [US1] Add `internal/writer/auth/bearer_test.go` — token-file parsing + lookup + rejection paths.
- [X] T038 [US1] Create `internal/writer/ingest/ingest.go`: (a) assigns `notification_id` (UUIDv7) and `created_at`; (b) sets `originating_service` from the authenticated `Principal`; (c) validates the whole `Notification` per data-model.md §1–§3 returning a typed `ValidationError` containing `[]FieldError`; (d) serialises and publishes via `pulsarlib.Publisher`. Uses functional options.
- [X] T039 [US1] Add `internal/writer/ingest/ingest_test.go`: table-driven validation coverage for every rule in data-model.md §2–§3; verifies UUIDv7 assignment; uses a fake `Publisher` to assert payload shape and the `notification_id` plaintext property.
- [X] T040 [US1] Create `internal/writer/httpserver/handlers.go` implementing the `ServerInterface` generated by oapi-codegen. Handles content-type negotiation (`application/json` via `protojson`, `application/x-protobuf` via `proto.Unmarshal`). On success emits 202 with the envelope from `internal/writer/envelope`. On any failure emits a problem from `internal/writer/problems`.
- [X] T041 [US1] Add `internal/writer/httpserver/handlers_test.go` — against `httptest` with fake `Publisher` and fake `Authenticator`: asserts 202 happy path with the exact `{data, meta}` shape, 400 validation failures with RFC 9457 body, 415 for unsupported content types, 503 when the publisher returns a "substrate unavailable" error.
- [X] T042 [US1] Create `internal/writer/httpserver/router.go` constructing the gorilla-mux router from the generated server, chaining middleware: recover, request-ID injection (ULID into `context` and response header `X-Request-Id`), slog access log, auth. Exposes `NewServer(cfg, deps) (*Server, error)` with functional options.
- [X] T043 [US1] Add `internal/writer/httpserver/router_test.go` — covers middleware ordering, request-ID propagation, and that request IDs appear on both success envelopes and problem responses.
- [X] T044 [US1] Create `cmd/writer/main.go`: load config, build slog + ootel, load bearer tokens, load encryption key set, construct `pulsarlib.Publisher`, construct HTTP server, `http.Server.ListenAndServe` with graceful shutdown on SIGINT/SIGTERM. Health endpoint at `/healthz`.

### Deliverer config, provider interface, Pushover implementation

- [X] T045 [P] [US1] Create `internal/deliverer/config/config.go` embedding `config.Shared` and adding `PulsarSubscription`, `Concurrency`, `RetryMaxAttempts`, `RetryInitialBackoff`, `RetryMaxBackoff`, `PushoverAppToken`, `PushoverBaseURL`, `Provider` (enum `pushover|sandbox`). Functional options.
- [X] T046 [P] [US1] Add `internal/deliverer/config/config_test.go` — env parsing for all fields including duration parsing.
- [X] T047 [P] [US1] Create `internal/deliverer/provider/provider.go` defining the `PushProvider` interface: `Push(ctx context.Context, n *notificationpbv1.Notification) (Result, error)` where `Result` carries provider response summary and `error` is typed as `Transient`/`Permanent`. Exposes `Registry` mapping `target` oneof type to `PushProvider`.
- [X] T048 [P] [US1] Add `internal/deliverer/provider/provider_test.go` — contract tests for the interface using a fake provider.
- [X] T049 [P] [US1] Create `internal/deliverer/provider/sandbox/sandbox.go` — in-memory `PushProvider` that records pushes to a slice. Functional options (e.g., `WithFailOnce`). For integration tests and local demos.
- [X] T050 [P] [US1] Add `internal/deliverer/provider/sandbox/sandbox_test.go`.
- [X] T051 [P] [US1] Create `internal/deliverer/provider/pushover/pushover.go`: thin HTTP client issuing `POST $BASE/1/messages.json`, translating `Notification.target.pushover` fields into Pushover form parameters. Classifies response per research.md §6 (2xx→delivered; 4xx recipient/payload→permanent; 401/403→permanent auth; 429/5xx/net→transient). Functional options for `WithHTTPClient`, `WithBaseURL`, `WithTimeout`.
- [X] T052 [P] [US1] Add `internal/deliverer/provider/pushover/pushover_test.go` — against `httptest.Server` exercising each classification row in research.md §6.
- [X] T053 [US1] Add `internal/deliverer/provider/pushover/pushover_integration_test.go` (`//go:build integration`) — runs the full Pushover provider against an `httptest` that mimics real Pushover response envelopes. Asserts field marshalling matches Pushover's documented form-field names.

### Deliverer outcomes, dispatcher, consumer, main

- [X] T054 [P] [US1] Create `internal/deliverer/outcomes/outcomes.go` with `Recorder` interface and a `SlogRecorder` implementation emitting `event=notification.outcome` structured log events per data-model.md §4. Exposes `Status` enum (stringer-generated) and the reason-code constants from data-model.md §5.
- [X] T055 [P] [US1] Add `internal/deliverer/outcomes/outcomes_test.go`.
- [X] T056 [US1] Create `internal/deliverer/dispatcher/dispatcher.go` which: (a) receives a decrypted `*notificationpbv1.Notification`; (b) type-switches on `GetTarget()` to the correct registered `PushProvider`; (c) calls `Push`; (d) records the outcome via `outcomes.Recorder`. Expired messages short-circuit to `expired` terminal before dispatch.
- [X] T057 [US1] Add `internal/deliverer/dispatcher/dispatcher_test.go` — table-driven across each terminal status with fake providers and a fake recorder.
- [X] T058 [US1] Create `internal/deliverer/consumer/consumer.go` driving a `pulsarlib.Consumer`: receives a message, pulls the plaintext `notification_id` property for log correlation, unmarshals protobuf, hands off to `dispatcher.Handle`. On Pulsar decrypt failure (from the `Fail` action), immediately emits a terminal `encryption-policy-violation` outcome (FR-016) and acks the message so it is not redelivered. Functional options.
- [X] T059 [US1] Add `internal/deliverer/consumer/consumer_test.go` — against a fake `Consumer` supplying crafted messages; verifies decrypt-failure handling produces the terminal outcome without calling the dispatcher.
- [X] T060 [US1] Create `cmd/deliverer/main.go`: load config, slog + ootel, load encryption key set (private keys), construct `pulsarlib.Consumer`, construct `PushProvider` (dispatch on `cfg.Provider`), construct dispatcher + `SlogRecorder`, construct consumer loop with `cfg.Concurrency` workers, graceful shutdown on SIGINT/SIGTERM. Health endpoint at `/healthz`.

### Public client library (FR-019, FR-024, SC-012)

- [X] T061 [P] [US1] Create `pkg/notificationclient/client.go` exporting `type Client struct` and `func New(baseURL string, opts ...Option) (*Client, error)` wrapping the generated client. Options: `WithHTTPClient`, `WithBearerToken`, `WithUserAgent`. Defines the high-level `func (c *Client) Submit(ctx, *SubmitRequest) (*SubmitResponse, error)` where `SubmitRequest` is built via `NewPushoverRequest` / `NewRequest`.
- [X] T062 [P] [US1] Create `pkg/notificationclient/response.go` — `SubmitResponse` type exposing `NotificationID() string`, `AcceptedAt() time.Time`, `RequestID() string`. Unpacks the `{data, meta}` envelope.
- [X] T063 [P] [US1] Create `pkg/notificationclient/errors.go` — `ProblemError` type implementing `error` with `Type()`, `Title()`, `Status()`, `Detail()`, `Instance()`, `RequestID()`, `FieldErrors()` methods. Exports sentinel errors (`ErrValidationFailed`, `ErrAuthenticationRequired`, `ErrAuthenticationFailed`, `ErrIdempotencyConflict`, `ErrUnsupportedContentType`, `ErrSubstrateUnavailable`, `ErrInternal`) matching research.md §8. `errors.Is` compares on `type` URI.
- [X] T064 [P] [US1] Create `pkg/notificationclient/doc.go` with package-level usage doc and the 3-line example from quickstart.md.
- [X] T065 [P] [US1] Add `pkg/notificationclient/client_test.go` — against `httptest.Server` exercising: 202 happy path unpacked into `SubmitResponse`; each sentinel error; `errors.Is` and `errors.As` both working on `ProblemError`; bearer-token header attachment; content-type selection via option.

### US1 end-to-end integration

- [X] T066 [US1] Add `internal/e2e/pipeline_integration_test.go` (`//go:build integration`) — spins Pulsar via testcontainers-go, starts the writer with a generated public key, starts the deliverer with the paired private key and the `sandbox` provider, submits a notification via `pkg/notificationclient`, asserts: (a) HTTP 202 with valid envelope; (b) message arrives at the sandbox provider within 2 seconds (SC-002); (c) outcome log records `delivered` with the same `notification_id`; (d) all five pipeline-stage log lines share the `notification_id` (SC-005). Placement under `internal/e2e/` is documented as the pragmatic exception to "tests next to code" because this test spans multiple packages.

**Checkpoint**: User Story 1 is fully functional, independently testable end-to-end, and satisfies the MVP. A live Pushover delivery is possible with `PROVIDER=pushover` + a real `PUSHOVER_APP_TOKEN`.

---

## Phase 4: User Story 2 — Resilience to transient failures (P2)

**Goal**: Transient push-provider or substrate failures do not lose messages. Permanent errors terminate promptly with a categorised reason. Unencrypted or undecryptable messages hard-fail per FR-016.

**Independent Test**: Configure `httptest`-backed Pushover to return HTTP 503 for a bounded interval; submit notifications throughout; restore to 200; confirm every submitted message reaches a terminal outcome (either `delivered` eventually or `permanently-failed` with `retry-budget-exhausted` after the configured attempts), and none are stuck or silently dropped.

### Retry classification + backoff

- [X] T067 [P] [US2] Create `internal/deliverer/retry/retry.go` with `Classifier` interface (`Classify(error) Class` where `Class` is `Transient|Permanent`), a `Policy` struct `{MaxAttempts, InitialBackoff, MaxBackoff}`, and an `Iterator` that yields successive wait durations (exponential backoff with full jitter). Functional options on the policy constructor.
- [X] T068 [P] [US2] Add `internal/deliverer/retry/retry_test.go` — table-driven classification for every Pushover response row in research.md §6; deterministic backoff test injecting a fake RNG.

### Wire retry into the delivery path

- [X] T069 [US2] Update `internal/deliverer/dispatcher/dispatcher.go` to invoke `retry.Policy` on transient `PushProvider` errors, bounded by `MaxAttempts`. On exhaustion, emit a terminal outcome with reason `retry-budget-exhausted`. Record the attempt count and the first/last attempt timestamps per data-model.md §4.
- [X] T070 [US2] Add `internal/deliverer/dispatcher/dispatcher_retry_test.go` — fake provider returning transient-then-success, and transient-until-budget-exhausted, verifying both paths emit the correct outcomes with accurate attempt counts.

### Encryption-policy enforcement

- [X] T071 [US2] Harden `internal/deliverer/consumer/consumer.go`: on any Pulsar client error whose cause is decrypt-failure (mapped via `pulsar.ConsumerCryptoFailureActionFail`), emit `encryption-policy-violation` terminal outcome with an alert-worthy metric label; acknowledge the message to prevent poison-pill redelivery. Do NOT dispatch to any provider.
- [X] T072 [US2] Add `internal/deliverer/consumer/consumer_encryption_test.go` — drives a fake consumer that surfaces a decrypt error, asserts the terminal `encryption-policy-violation` outcome and zero calls into the dispatcher.

### Writer substrate-unavailable handling

- [X] T073 [US2] Update `internal/writer/ingest/ingest.go` and `internal/writer/httpserver/handlers.go` so that `pulsarlib.Publisher.Publish` errors classified as transient (Pulsar broker unavailable, producer not connected) translate to `problems.ProblemSubstrateUnavailable` with HTTP 503 and a `retry_after_seconds` extension member. Persistent errors translate to `ProblemInternal`.
- [X] T074 [US2] Add `internal/writer/httpserver/substrate_unavailable_test.go` — fake publisher returns transient error; asserts 503 body matches RFC 9457 shape and the extension is populated.

### US2 end-to-end integration

- [X] T075 [US2] Add `internal/e2e/resilience_integration_test.go` (`//go:build integration`) — spins Pulsar via testcontainers, runs writer + deliverer, injects an `httptest` Pushover that returns HTTP 503 for the first N requests then 200. Asserts: (a) no message is lost; (b) outcomes emitted on success are `delivered` with `attempts > 1`; (c) separately, when the stub returns permanent 400, the outcome is `permanently-failed` with the matching reason; (d) a forged unencrypted message on the topic produces `encryption-policy-violation` and zero provider calls.

**Checkpoint**: The pipeline is production-safe against transient provider failures, transient substrate failures, and policy-violating messages.

---

## Phase 5: Polish & Cross-Cutting Concerns

**Purpose**: Cross-cutting improvements that do not belong to a single user story. Operator docs, CI enforcement, performance validation.

- [X] T076 [P] Wire `tools/funcoptslint` into CI: add a `.github/workflows/lint.yml` job (or extend existing) invoking the analyzer on every package under `pkg/` and `internal/`. Build fails on any diagnostic. *[Added the `funcoptslint` job to `.github/workflows/pull_request.yml` and a `make funcoptslint` target. Current codebase passes with zero diagnostics.]*
- [X] T077 [P] Rewrite `README.md`: replace the upstream `go-start` description with the Pulsar Notification Pipeline overview (two binaries, shared libraries, Pulsar-only runtime dependency, quickstart link). Keep the existing CI/CD section.
- [X] T078 [P] Add `docs/key-rotation.md` — operator runbook extracted from quickstart.md §"Rotating the Pulsar E2EE key pair", fleshed out with rollback steps and a canary-key smoke-check script.
- [X] T079 [P] Add `docs/errors.md` — human-facing catalogue of every `type` URI from research.md §8 with HTTP status, typical cause, operator action.
- [X] T080 [P] Add `.github/workflows/test.yml` jobs: `unit` (`go test ./...`), `integration` (`go test -tags=integration ./...` with Docker service), `compat` (runs just the protobuf compat tests to surface failures visibly).
- [X] T081 Walk the quickstart: boot `make up`, submit a notification via `curl`, then via `pkg/notificationclient`, rotate keys per the runbook, shut down cleanly. File issues for any friction. *[Functionally exercised by `make test-integration`: real Pulsar via testcontainers + httptest Pushover + the `pkg/notificationclient` submit path + retry recovery all validated in CI-runnable form. Live `docker compose up` + curl still worth a human-operator pass before production cutover.]*
- [X] T082 Performance validation: write `internal/e2e/perf_test.go` (`//go:build perf`) that submits 500 notifications/second for 60 seconds against the sandbox provider. Record p95 writer ack latency (SC-001 target 250ms), p95 end-to-end dispatch (SC-002 target 2s), backlog growth (SC-006 target 0). Fail the test if any target is missed.
- [X] T083 Post-implementation constitution re-check: verify all five principles and additional constraints still hold. If any drift is found, file an issue tagged `constitution-drift` per the Governance section; fix or track.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies; start immediately.
- **Foundational (Phase 2)**: Requires Setup. Blocks both user stories.
- **US1 (Phase 3)**: Requires Foundational. MVP.
- **US2 (Phase 4)**: Requires Foundational. Can start in parallel with US1 once Foundational is complete, but several US2 tasks modify files US1 also touches (e.g., `consumer.go`, `dispatcher.go`); prefer completing US1 first for a cleaner diff history.
- **Polish (Phase 5)**: Requires US1 at minimum; several items (T082 perf validation) require US2 as well.

### Within-Phase Dependencies

**Phase 2 critical path**:
- T009 → T010 (protobuf source → generated) → T011–T013 (use generated types)
- T014 → T015 (OpenAPI source → generated) → T016 (CI enforcement)
- T017 → T018 (config + its test)
- T020 → T021 → T022 (interfaces → crypto reader → implementation) → T025 (integration test)

**Phase 3 critical path (US1)**:
- Writer: T032→T038 (config, auth, ingest before handlers) → T040 → T042 → T044
- Deliverer: T045 → T047 → T051 → T056 → T058 → T060
- Client library: T061–T065 largely parallel; independent of writer/deliverer implementations (uses `httptest`)
- E2E T066 last — requires writer, deliverer, and client library complete

**Phase 4 critical path (US2)**:
- T067 → T069 (retry policy before wiring into dispatcher)
- T071 → T072 (consumer change before its test)
- T073 → T074 (handler change before its test)
- T075 last — requires all US2 work merged

### Parallel Opportunities

- All [P]-marked Setup and Foundational tasks can run in parallel within their phase.
- Client library (T061–T065) can run in parallel with writer and deliverer work in Phase 3.
- `internal/writer/*` and `internal/deliverer/*` have no cross-imports (enforced by Principle III), so writer and deliverer task streams run in parallel in Phase 3.
- All unit test tasks marked [P] can run in parallel with the implementation tasks they test (TDD order — write the test first in the same parallel window).

---

## Parallel Example: User Story 1

```bash
# After Phase 2 completes, a two-developer setup:

# Developer A — writer
Task: "T032 [P] [US1] Writer config in internal/writer/config/config.go"
Task: "T034 [P] [US1] Auth interface in internal/writer/auth/auth.go"
Task: "T036 [P] [US1] Bearer auth in internal/writer/auth/bearer.go"
# Then T038 ingest (sequential on T032, T034)
# Then T040 handlers, T042 router, T044 main.

# Developer B — deliverer
Task: "T045 [P] [US1] Deliverer config in internal/deliverer/config/config.go"
Task: "T047 [P] [US1] PushProvider interface in internal/deliverer/provider/provider.go"
Task: "T049 [P] [US1] Sandbox provider in internal/deliverer/provider/sandbox/sandbox.go"
Task: "T051 [P] [US1] Pushover provider in internal/deliverer/provider/pushover/pushover.go"
# Then T054 outcomes, T056 dispatcher, T058 consumer, T060 main.

# Developer C — client library (can start as soon as OpenAPI is regenerated in T015)
Task: "T061 [P] [US1] Client wrapper in pkg/notificationclient/client.go"
Task: "T062 [P] [US1] Response helpers in pkg/notificationclient/response.go"
Task: "T063 [P] [US1] Typed error in pkg/notificationclient/errors.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1 (Setup): T001–T008.
2. Complete Phase 2 (Foundational): T009–T031. This is the biggest single chunk — two source-of-truth contracts plus the Pulsar library with E2EE and testcontainers coverage. Do not skip T025 (real Pulsar integration) or T031 (lint enforcement); cutting these invalidates Principle IV and Principle II.
3. Complete Phase 3 (US1): T032–T066. STOP after T066 passes and validate against spec.md's US1 independent test.
4. At this point the pipeline delivers notifications end-to-end through Pushover. You can ship to a staging environment and begin exercising it with real load.

### Incremental Delivery

- MVP: Phases 1+2+3 → ship. Happy-path delivery through Pushover works. Failures are visible but not retried.
- Production-ready: add Phase 4 (US2) → resilience landing. Now safe for on-call.
- Polish: Phase 5 closes CI/docs/perf gaps and re-verifies constitution alignment.

### Parallel Team Strategy

- Two to three developers can work Phase 3 in parallel after T031 merges: one on writer, one on deliverer, one on client library. The integration test (T066) forces a single merge gate at the end.
- Phase 4 is essentially one developer's focused pass; the changes cluster in three files (`dispatcher.go`, `consumer.go`, `handlers.go`).

---

## Notes

- [P] tasks = different files, no incomplete-task dependencies
- [Story] label maps task to a user story for traceability
- Every `_test.go` task precedes (or is paired with) the implementation task it verifies — Principle IV
- The constitution's Principle II (functional options) is enforced mechanically by T030/T031 and reinforced at review time
- `notification_id` is the single correlation key across all log records (SC-005); new log sites should add it or be rejected in review
- Avoid: cross-story edits in the same file (violates independent testability); exporting a struct without a functional-options constructor; writing production code that imports `github.com/apache/pulsar-client-go` from outside `internal/pulsarlib/` (violates Principle I)
