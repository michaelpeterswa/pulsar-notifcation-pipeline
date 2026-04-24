# pulsar-notifcation-pipeline

End-to-end push notification pipeline: HTTP API → Apache Pulsar (E2EE) → push provider.

Two Go binaries plus shared libraries. The writer accepts notifications over HTTP,
validates them, encrypts with Pulsar native client-side E2EE, and publishes to a topic.
The reader/deliverer consumes, decrypts, and dispatches to the configured push provider
(Pushover at MVP; interface-first so new providers slot in cleanly).

## Highlights

- **Writer** (`cmd/writer/`) — gorilla/mux HTTP API, protobuf or JSON request bodies,
  `{data, meta}` JSON success envelope, RFC 9457 `application/problem+json` error
  envelope, bearer-token auth, request-ID propagation.
- **Reader/Deliverer** (`cmd/deliverer/`) — Pulsar consumer + decrypt, push-provider
  dispatcher with bounded exponential-backoff retry, terminal-outcome recording.
- **Shared protobuf library** (`pkg/notificationpb/v1/`) — the canonical wire contract
  on Pulsar. Forward/backward wire-compatibility enforced by a compat test
  (`pkg/notificationpb/v1/compat_test.go`).
- **Public Go HTTP client** (`pkg/notificationclient/`) — generated from the same
  OpenAPI document (`api/openapi.yaml`) as the server stubs, wrapped with
  functional-options constructor, typed response accessors, and typed RFC 9457 errors.
- **Shared Pulsar library** (`internal/pulsarlib/`) — interface-first wrapper over
  `github.com/apache/pulsar-client-go/pulsar` with native E2EE.
- **Only runtime infrastructure dependency is Apache Pulsar.** No database, no Redis,
  no secret manager. Keys are mounted files; auth tokens are a mounted file.

## Quickstart

See [specs/001-notification-pipeline/quickstart.md](specs/001-notification-pipeline/quickstart.md)
for the authoritative walkthrough (docker-compose stack, curl example, Go client
example, key-rotation runbook).

```bash
# Generate OpenAPI + protobuf artefacts (after editing api/openapi.yaml or
# proto/notification/v1/notification.proto)
just generate

# Unit tests
just test

# Integration tests (requires Docker — spins up Apache Pulsar via testcontainers)
just test-integration

# Local stack: Pulsar + writer + deliverer + Grafana LGTM
just up
```

## Submit a notification via the Go client

```go
c, _ := notificationclient.New("http://localhost:8080",
    notificationclient.WithBearerToken("local-dev-token"))
resp, err := c.Submit(ctx, notificationclient.NewPushoverRequest(
    "uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "Hello", "from Go"))
// resp.NotificationID(), resp.RequestID(), resp.AcceptedAt()
```

## Configuration

Each binary is configured via environment variables (per Constitution V). See
`specs/001-notification-pipeline/quickstart.md#environment-reference` for the full
list. Highlights:

| Var | Binary | Purpose |
|-----|--------|---------|
| `PULSAR_SERVICE_URL` | both | e.g. `pulsar://pulsar:6650` |
| `PULSAR_TOPIC` | both | e.g. `persistent://public/default/notifications` |
| `PULSAR_ENCRYPTION_KEY_DIR` | both | Directory of `<name>.pub.pem` / `<name>.priv.pem` |
| `PULSAR_ENCRYPTION_KEY_NAMES` | writer | CSV of active public key names (supports rotation overlap) |
| `WRITER_HTTP_ADDR` | writer | HTTP listen address |
| `WRITER_BEARER_TOKENS_FILE` | writer | Path to tokens file |
| `PUSHOVER_APP_TOKEN` | deliverer | Pushover application API token |
| `DELIVERER_RETRY_MAX_ATTEMPTS` | deliverer | Transient-error retry budget |

## Observability

Both binaries bootstrap OpenTelemetry via `alpineworks.io/ootel`:

- **Metrics**: custom pipeline metrics (submissions, outcomes, provider latency,
  retry attempts, encryption-policy violations) + Go runtime + host metrics.
  Names live in `internal/metrics/metrics.go`. In `LOCAL=true` mode they push
  via OTLP gRPC to the local LGTM stack; in production they're scraped from
  `:METRICS_PORT/metrics`.
- **Traces**: `TRACING_ENABLED=true` turns on OTLP gRPC span export. Sample
  rate is `TRACING_SAMPLERATE`.
- **Logs**: a [Grafana Alloy](https://grafana.com/docs/alloy/) sidecar in the
  compose stack tails every container's stdout via the Docker socket,
  labels each stream by compose `service`, and ships to the bundled Loki in
  the LGTM container. Query in Grafana with e.g.
  `{service=~"writer|deliverer"} |= "019dc0cc..."` to correlate a single
  `notification_id` across every pipeline stage (SC-005). Config:
  `docker/alloy/config.alloy`.
- **Grafana dashboard**: `docker/grafana/dashboards/pipeline.json` is
  auto-provisioned; open http://localhost:3000 once `just up` is running and
  look for "Pulsar Notification Pipeline" under Dashboards. Panels cover
  submission rate/latency, consumed/delivered/failed rates, outcomes by
  reason, provider latency, attempts heatmap, and encryption-policy-violation
  count (the FR-016 alert target).

## Design docs

- **Constitution** — `.specify/memory/constitution.md` (five core principles)
- **Feature spec** — `specs/001-notification-pipeline/spec.md`
- **Implementation plan** — `specs/001-notification-pipeline/plan.md`
- **Research / decisions** — `specs/001-notification-pipeline/research.md`
- **Data model** — `specs/001-notification-pipeline/data-model.md`
- **Contracts** — `specs/001-notification-pipeline/contracts/`
- **Tasks** — `specs/001-notification-pipeline/tasks.md`
- **Secrets architecture & deployment** — [docs/secrets.md](docs/secrets.md)
- **Key rotation runbook** — [docs/key-rotation.md](docs/key-rotation.md)
- **Error catalogue** — [docs/errors.md](docs/errors.md)

## CI/CD

PRs are validated with:

- `go test ./...` — unit tests (fast)
- `go test -tags=integration ./...` — integration tests (testcontainers + httptest)
- `golangci-lint` — linting
- `go vet`
- OpenAPI and protobuf regeneration drift check (`just generate` produces zero diff)
- `commitlint` — Conventional Commit message enforcement
- `yamllint`, `hadolint`
