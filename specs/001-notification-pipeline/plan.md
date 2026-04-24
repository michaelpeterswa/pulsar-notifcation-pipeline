# Implementation Plan: End-to-End Push Notification Pipeline

**Branch**: `001-notification-pipeline` | **Date**: 2026-04-23 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-notification-pipeline/spec.md`

## Summary

Ship two Go binaries and three shared libraries that implement a notification pipeline
from an HTTP API through Apache Pulsar to Pushover.

1. **Writer** (`cmd/writer/`) exposes an HTTP API (request bodies in protobuf or JSON),
   validates submissions, authenticates callers, and publishes the canonical notification
   message to Pulsar using Pulsar native E2EE under a configured public key. Responses are
   always JSON: successes use a `{data, meta}` envelope, errors use RFC 9457
   `application/problem+json`.
2. **Reader/Deliverer** (`cmd/deliverer/`) consumes from Pulsar, decrypts with its
   private key, dispatches to the configured push provider (Pushover at MVP), records
   a terminal outcome, and hard-rejects any unencrypted or undecryptable message.
3. **Shared protobuf library** (`proto/notification/v1/` → generated into `pkg/notificationpb/v1/`)
   — the sole wire contract on Pulsar. Per-provider fields are carried via a protobuf
   `oneof target` arm so new providers can be added without schema breakage.
4. **Shared Go HTTP client library** (`pkg/notificationclient/`) — generated from the
   OpenAPI document at `api/openapi.yaml` so that upstream Go services can submit
   notifications in ≤3 lines of call-site code.
5. **Shared Pulsar communication library** (`internal/pulsarlib/`) — interface-first
   wrapper over `github.com/apache/pulsar-client-go` covering producer/consumer
   construction, E2EE key loading, and lifecycle.

The only runtime infrastructure dependency is Apache Pulsar; everything else is
in-process Go code. Observability is slog + OpenTelemetry per the constitution.

## Technical Context

**Language/Version**: Go 1.25+ (requires `go.mod` bump from the current 1.22 — see
Complexity Tracking). Rationale for 1.25 floor: `range-over-func` is stable, `math/rand/v2`
and `log/slog` have matured, and no existing code depends on anything pre-1.25.

**Primary Dependencies** (first-party Go libraries; *Pulsar is the only external runtime
infrastructure dependency*):

- `github.com/apache/pulsar-client-go/pulsar` — Pulsar client with native E2EE support
- `github.com/gorilla/mux` — HTTP router for the writer
- `github.com/caarlos0/env/v11` — env-based config (already in `go.mod`; per Constitution V)
- `alpineworks.io/ootel` — OpenTelemetry bootstrap (already in `go.mod`)
- `github.com/alpineworks/rfc9457` — server-side RFC 9457 problem document emission (FR-022)
- `github.com/oapi-codegen/oapi-codegen/v2` (build-time) — generates server stubs AND the
  `pkg/` Go client library from one OpenAPI document (FR-018, FR-019)
- `google.golang.org/protobuf` + `protoc-gen-go` (build-time) — canonical message schema (FR-001)
- `log/slog` — stdlib structured logging

No Pushover SDK is imported; the Pushover provider is a thin HTTP wrapper in
`internal/deliverer/provider/pushover/` (Pushover's Messages API is a single `POST
https://api.pushover.net/1/messages.json` call with ~10 form fields). This keeps the
dependency surface minimal per Constitution's dependency hygiene clause.

**Storage**: None at MVP. The pipeline is stateless; Pulsar topic state is the only
persistence. No DB, no Redis, no Vault. Keys are mounted files (FR-017).

**Testing**: `go test ./...` for unit tests (tests beside code per the constitution).
Integration tests use `github.com/testcontainers/testcontainers-go` with the
`apachepulsar/pulsar` image for the Pulsar library and the E2E decrypt path; Pushover
integration is tested against an `httptest.Server` that stands in for
`api.pushover.net` (Pushover has no local test container, and a real sandbox round-trip
would flap CI). Integration tests are guarded by the `integration` build tag.

**Target Platform**: Linux containers (multi-stage build; distroless final image — Dockerfile
scaffolding already in repo and will be adapted for both binaries).

**Project Type**: Multi-binary Go module — two `cmd/` applications (writer, deliverer)
plus three shared libraries (protobuf types in `pkg/notificationpb`, generated HTTP client
in `pkg/notificationclient`, Pulsar wrapper in `internal/pulsarlib`).

**Performance Goals** (from spec):

- Writer HTTP ack: p95 ≤ 250 ms (SC-001)
- End-to-end to Pushover: p95 ≤ 2 s (SC-002)
- Sustained throughput: ≥ 500 messages/s per instance without growing backlog (SC-006)
- ≥ 99.9% terminal-outcome rate under nominal load (SC-003)

**Constraints**:

- Pulsar native E2EE (FR-015, FR-016); the canonical protobuf is the plaintext; brokers
  see only ciphertext plus the plaintext notification-ID property.
- Responses always JSON (FR-023); request bodies may be protobuf or JSON (FR-002).
- No device registry (FR-008); caller supplies the target Pushover user/group key.
- Functional-options pattern on every exported struct (Constitution II, non-negotiable).
- Push-provider access is always behind a Go interface (Constitution I) so the second
  provider can be added without touching the writer, substrate, or protobuf schema
  (SC-008).

**Scale/Scope**: One (or small pool of) instances per environment; multi-region and
hundreds-of-instance scale are explicitly out of scope.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design — results
recorded at the end of this section.*

Evaluated against the five principles ratified in `.specify/memory/constitution.md` v1.0.0
and the "Additional Constraints" section.

| # | Principle | Gate | Status |
|---|-----------|------|--------|
| I | Interface-First Design | Every capability behind a Go interface; concrete types are one implementation. | **Complies.** Three explicit interface seams: `pulsarlib.Publisher`/`pulsarlib.Consumer` (substrate), `provider.PushProvider` (delivery), and `outcomes.Recorder` (terminal-outcome sink). Clients depend on the interface in every case. |
| II | Functional Options (NON-NEGOTIABLE) | All exported constructors use `New<T>(required, opts ...Option) (*T, error)`. | **Complies.** Plan requires every constructor on exported types — `notificationclient.New`, `pulsarlib.NewPublisher`, `pulsarlib.NewConsumer`, `pushover.New`, `writer.NewServer`, `deliverer.NewDispatcher` — to use the options pattern. Enforced by code review and by structural linting in CI (see research.md §9). |
| III | Writer / Reader-Deliverer Separation | Two binaries; no cross-app imports; wire contract through shared packages. | **Complies.** `cmd/writer/` imports only `internal/writer/**`, `internal/pulsarlib`, and the shared `pkg/notificationpb`. `cmd/deliverer/` imports only `internal/deliverer/**`, `internal/pulsarlib`, and the shared `pkg/notificationpb`. Enforced by an import-boundary lint rule. |
| IV | Test Discipline (Unit + Testcontainers) | Every exported function tested; external-dep code uses testcontainers-go. | **Complies.** Pure logic (protobuf envelope handling, retry/backoff, problem-document emission, response envelope, client decoding) covered by unit tests. Pulsar and the Pushover HTTP round-trip covered by testcontainers/httptest integration tests under the `integration` build tag. |
| V | Observability & Environment-Driven Configuration | slog + OTel + env-only config via `caarlos0/env`. | **Complies.** Single config struct per binary; parsed once at startup from the environment. All code emits through `log/slog` and the existing `alpineworks.io/ootel` bootstrap; no `fmt.Println` or ad-hoc `os.Getenv` outside the config package. |

Additional constraints:

| Constraint | Status |
|------------|--------|
| Go toolchain per `go.mod` | Planned bump from 1.22 → 1.25+ (user-requested); documented in Complexity Tracking. |
| Substrate-specific code behind an interface | Complies — `internal/pulsarlib` is the single place that imports the Pulsar client SDK. |
| Layout (`cmd/<app>/`, `internal/`, `pkg/`) | Complies — see Project Structure. |
| Dependency hygiene | Complies — new deps (pulsar-client-go, gorilla/mux, alpineworks/rfc9457, oapi-codegen, protoc-gen-go) are each justified in research.md. |
| Conventional Commits, golangci-lint, yamllint, hadolint, go test in CI | Complies — existing CI scaffolding extended, not replaced. |

**Initial gate result: PASS.** One planned governance action (Go version bump) is tracked
in Complexity Tracking.

**Post-Phase-1 gate re-check**: PASS. The concrete data model, contracts, and quickstart
introduced no additional interface seams beyond those declared above and did not require
any new exceptions.

## Project Structure

### Documentation (this feature)

```text
specs/001-notification-pipeline/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── openapi.yaml     # Writer HTTP API contract (source of truth for server + pkg/ client)
│   └── notification.proto  # Canonical message schema (source of truth for pkg/notificationpb)
├── checklists/
│   └── requirements.md  # From /speckit.specify validation
└── tasks.md             # Phase 2 output (from /speckit.tasks)
```

### Source Code (repository root)

```text
.
├── api/
│   └── openapi.yaml                 # Generated into internal/writer/httpserver + pkg/notificationclient
├── proto/
│   └── notification/
│       └── v1/
│           └── notification.proto   # Generated into pkg/notificationpb/v1
├── cmd/
│   ├── writer/
│   │   └── main.go                  # Writer entrypoint; wires config, pulsarlib publisher, HTTP server
│   └── deliverer/
│       └── main.go                  # Reader/deliverer entrypoint; wires config, pulsarlib consumer, provider
├── pkg/
│   ├── notificationpb/
│   │   └── v1/
│   │       ├── notification.pb.go   # Generated from proto/notification/v1/notification.proto
│   │       └── doc.go               # Package documentation
│   └── notificationclient/
│       ├── generated/
│       │   └── client.gen.go        # oapi-codegen client output
│       ├── client.go                # Functional-options constructor + thin wrapper
│       ├── response.go              # Typed accessors for {data, meta} (FR-024)
│       ├── errors.go                # Typed RFC 9457 error impl + sentinel errors (FR-024)
│       └── client_test.go           # Unit tests against httptest
├── internal/
│   ├── config/                      # Shared env config helpers (already present; extend)
│   ├── logging/                     # Shared slog setup (already present)
│   ├── pulsarlib/                   # Shared Pulsar library (FR-020)
│   │   ├── pulsarlib.go             # Publisher/Consumer interfaces + Option type
│   │   ├── apachepulsar.go          # Apache Pulsar implementation
│   │   ├── keys.go                  # Key-material loading from env-referenced files (FR-017)
│   │   ├── pulsarlib_test.go        # Unit tests (interface contracts)
│   │   └── apachepulsar_integration_test.go  # //go:build integration — testcontainers
│   ├── writer/
│   │   ├── config/                  # Writer-specific env config struct
│   │   ├── httpserver/
│   │   │   ├── server.gen.go        # oapi-codegen server output
│   │   │   ├── router.go            # gorilla/mux wiring + middleware stack
│   │   │   ├── handlers.go          # Implements generated server interface
│   │   │   └── handlers_test.go
│   │   ├── auth/
│   │   │   ├── auth.go              # Interface + middleware
│   │   │   └── bearer.go            # Bearer-token implementation (see Deferred items)
│   │   ├── ingest/
│   │   │   ├── ingest.go            # Validation + publish pipeline
│   │   │   └── ingest_test.go
│   │   ├── problems/
│   │   │   ├── problems.go          # Shapes writer errors into RFC 9457 via alpineworks/rfc9457
│   │   │   └── problems_test.go
│   │   └── envelope/
│   │       ├── envelope.go          # {data, meta} envelope construction
│   │       └── envelope_test.go
│   └── deliverer/
│       ├── config/                  # Deliverer-specific env config struct
│       ├── consumer/
│       │   ├── consumer.go          # Pulsar consume + decrypt + hand-off to dispatcher
│       │   └── consumer_test.go
│       ├── provider/
│       │   ├── provider.go          # PushProvider interface (Interface-First)
│       │   ├── pushover/
│       │   │   ├── pushover.go      # Pushover implementation (thin HTTP wrapper)
│       │   │   ├── options.go       # Functional options
│       │   │   ├── pushover_test.go
│       │   │   └── pushover_integration_test.go  # //go:build integration — httptest
│       │   └── sandbox/
│       │       └── sandbox.go       # In-memory provider for tests and local demos
│       ├── dispatcher/
│       │   ├── dispatcher.go        # Routes Notification.oneof → selected PushProvider
│       │   └── dispatcher_test.go
│       ├── retry/
│       │   ├── retry.go             # Bounded backoff classifier
│       │   └── retry_test.go
│       └── outcomes/
│           ├── outcomes.go          # Recorder interface + structured-log implementation
│           └── outcomes_test.go
├── docker/                          # Grafana LGTM stack (already present)
├── Dockerfile                       # Multi-stage; two targets (writer, deliverer)
├── docker-compose.yml               # Local dev: Pulsar + LGTM + both apps
├── Makefile                         # Regenerate proto + openapi; run tests
├── buf.gen.yaml                     # protoc-gen-go config
├── oapi-codegen.yaml                # Generator config for server + client
└── go.mod                           # Go 1.25+
```

**Structure Decision**: Multi-binary Go module with a shared exported surface
(`pkg/notificationpb/v1`, `pkg/notificationclient`) and a shared internal surface
(`internal/pulsarlib`, `internal/config`, `internal/logging`). Writer-only and
deliverer-only packages live under `internal/writer/**` and `internal/deliverer/**`
respectively, and each `cmd/<app>/main.go` imports only its own subtree plus the shared
packages — enforcing Constitution Principle III at the package-graph level.

The protobuf source lives under `proto/notification/v1/` (not `pkg/`) because `proto/` is
build input, not an importable Go package. Generated Go lands in `pkg/notificationpb/v1/`
which IS importable by external consumers — the protobuf library is a public artefact
(FR-001). The OpenAPI document lives at `api/openapi.yaml` (conventional) with generated
code split between `internal/writer/httpserver/` (server stubs — private) and
`pkg/notificationclient/generated/` (client — public).

## Complexity Tracking

| Violation / Deviation | Why Needed | Simpler Alternative Rejected Because |
|-----------------------|------------|--------------------------------------|
| Bump `go.mod` from Go 1.22 → 1.25+ | User requested Go 1.25+; no existing code depends on 1.22 semantics; 1.25 includes stable `range-over-func`, `math/rand/v2`, and mature `log/slog`. The constitution flags toolchain bumps as governance changes, so this is tracked here rather than performed silently. | Staying on 1.22 would deny the user's explicit request and forgo stdlib improvements. No ecosystem dependency of this project pins to 1.22. |
| Three shared libraries instead of one | `notificationpb` is the Pulsar wire contract, `notificationclient` is the HTTP call shape, `pulsarlib` is a substrate wrapper — they have different consumers (external vs. writer-only vs. both binaries) and different generation lifecycles (protoc vs. oapi-codegen vs. hand-written). Collapsing them would force one generator to own the other's output. | A single `pkg/notification` package was considered and rejected because the HTTP client and the Pulsar substrate wrapper would drag each other into every consumer's dependency graph. |
