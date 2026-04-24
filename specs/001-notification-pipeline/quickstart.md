# Quickstart: End-to-End Push Notification Pipeline

**Feature**: `001-notification-pipeline`

This is the operator's and contributor's getting-started guide. It is written against the
planned layout in `plan.md`; some paths do not yet exist on this branch until the tasks
from `/speckit.tasks` have been implemented.

---

## Prerequisites

- **Go 1.25+** (see `go.mod`; bumped as part of this feature — see plan.md Complexity Tracking).
- **`just`** (https://github.com/casey/just) — the developer task runner. `brew install just` on macOS.
- **Docker** (for `testcontainers-go` integration tests and the local `docker-compose.yml`).
- **`protoc`** (e.g. `brew install protobuf`) — used by `just generate` to regenerate
  `pkg/notificationpb/v1/notification.pb.go`. The Go-side plugin `protoc-gen-go` is built
  locally under `./bin/` by the recipe, so no global install is required for it.
- **`openssl`** — used by `just dev-secrets` to generate the local RSA key pair.
- A Pushover **application API token** (for real delivery) — paste into
  `PUSHOVER_APP_TOKEN` in your local `.env`. For CI / integration tests, use the
  built-in `sandbox` provider (`PROVIDER=sandbox`).
- A test **Pushover user key** — paste into the request body when sending test messages.

---

## One-command local stack

```bash
just up
```

The `up` recipe runs `dev-secrets` first, which generates a local RSA key pair
at `.secrets/pulsar-keys/v1.{pub,priv}.pem` and a bearer-tokens file at
`.secrets/writer-tokens` with a single `local-dev-token` entry. The
`.secrets/` tree is gitignored; re-running `just dev-secrets` is idempotent.
Optional overrides (Pushover token, log level, real-vs-sandbox provider)
live in `.env` — copy `.env.example` to `.env` and edit.

This brings up, all with healthchecks and restart-on-failure policies:

- `pulsar` — Apache Pulsar 3.3 in standalone mode (`pulsar://localhost:6650`);
  persistent `pulsar-data` volume so topic state survives restarts.
- `writer` — HTTP API on `:8080`, metrics on `:8181`; blocks starting until
  Pulsar reports healthy. Bearer-token auth enabled (`local-dev-token`).
- `deliverer` — consumer + dispatcher; metrics on `:8282`. Defaults to the
  sandbox provider so the stack works without a real Pushover account.
- `grafana-lgtm` — Grafana on `:3000`, OTLP gRPC on `:4317`. Writer and
  deliverer push traces + metrics here automatically.

---

## Submit a notification (curl, JSON body)

The wire format is proto3 canonical JSON of the canonical Notification
message. Oneof arms (here `pushover`) sit directly at the top level, NOT
under a `target` wrapper — that's how proto3 serialises `oneof` in JSON.

```bash
curl -sS -X POST http://localhost:8080/notifications \
  -H 'Authorization: Bearer local-dev-token' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: first-run-0001' \
  --data-binary @- <<'JSON'
{
  "content": {
    "title": "Hello from the pipeline",
    "body":  "If you see this on your Pushover client, the E2E is green.",
    "priority": 0,
    "data": { "source": "quickstart" }
  },
  "pushover": {
    "userOrGroupKey": "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
    "sound": "pushover"
  }
}
JSON
```

Expected response (pretty-printed):

```json
{
  "data": {
    "notificationId": "01933dcc-2b8f-7d44-9aab-52c6f1c0a5d7",
    "acceptedAt":     "2026-04-23T18:00:00.123Z"
  },
  "meta": {
    "requestId": "req_01HS98TQKJ5PXQZ5N8C3GD6G1N"
  }
}
```

Grep the deliverer logs for the returned `notificationId` to trace the dispatch.

---

## Submit a notification (Go, via `pkg/notificationclient`)

Three-line call site (SC-012):

```go
c, _ := notificationclient.New("http://localhost:8080",
    notificationclient.WithBearerToken("local-dev-token"))
resp, err := c.Submit(ctx, notificationclient.NewPushoverRequest(
    "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
    "Hello", "via the client"))
fmt.Println(resp.NotificationID(), err)
```

Typed error handling (FR-024):

```go
if err != nil {
    var perr *notificationclient.ProblemError
    if errors.As(err, &perr) && errors.Is(perr, notificationclient.ErrValidationFailed) {
        for _, fe := range perr.FieldErrors() {
            log.Printf("field=%s code=%s: %s", fe.Field, fe.Code, fe.Message)
        }
    }
}
```

---

## Environment reference

Per-binary environment variables. Shared keys appear on both.

> For the "where does each secret live in production?" story — including
> the fan-notifier example, k8s/Vault/bare-metal layouts, and operator
> checklists for onboarding/revoking/rotating each credential — see
> [../../docs/secrets.md](../../docs/secrets.md).

### Shared

| Var | Default | Notes |
|-----|---------|-------|
| `LOG_LEVEL` | `info` | debug / info / warn / error |
| `METRICS_PORT` | `8081` | Prometheus or OTLP egress |
| `TRACING_ENABLED` | `false` | |
| `PULSAR_SERVICE_URL` | `pulsar://localhost:6650` | |
| `PULSAR_TOPIC` | `persistent://public/default/notifications` | |
| `PULSAR_ENCRYPTION_KEY_DIR` | `/run/secrets/pulsar-keys` | Directory of `<name>.pub.pem` / `<name>.priv.pem` files |

### Writer

| Var | Default | Notes |
|-----|---------|-------|
| `WRITER_HTTP_ADDR` | `:8080` | |
| `WRITER_BEARER_TOKENS_FILE` | `/run/secrets/writer-tokens` | One `<token> [service-name]` per line |
| `WRITER_IDEMPOTENCY_WINDOW` | `5m` | |
| `PULSAR_ENCRYPTION_KEY_NAMES` | `v1` | Comma-separated; during rotation set to `v1,v2` |

### Deliverer

| Var | Default | Notes |
|-----|---------|-------|
| `PULSAR_SUBSCRIPTION` | `notifications-deliverer` | |
| `DELIVERER_CONCURRENCY` | `16` | |
| `DELIVERER_RETRY_MAX_ATTEMPTS` | `5` | |
| `DELIVERER_RETRY_INITIAL_BACKOFF` | `500ms` | |
| `DELIVERER_RETRY_MAX_BACKOFF` | `30s` | |
| `PUSHOVER_APP_TOKEN` | *(required)* | Application token from Pushover console |
| `PUSHOVER_BASE_URL` | `https://api.pushover.net` | Override for testing (e.g., httptest) |
| `PROVIDER` | `pushover` | `pushover` \| `sandbox` (for local E2E without a real Pushover account) |

---

## Regenerating contract artefacts

Whenever you edit `api/openapi.yaml` or `proto/notification/v1/notification.proto`:

```bash
just generate
```

This runs `protoc` + `protoc-gen-go` for the protobuf library and `oapi-codegen` for both
the server stubs and the client library. CI re-runs this and fails the build on
uncommitted diffs (SC-011: zero manual diffs).

---

## Rotating the Pulsar E2EE key pair

1. Generate a new RSA 2048 key pair, named e.g. `v2`:

   ```bash
   # The Pulsar Go client's default message crypto parses private keys via
   # x509.ParsePKCS1PrivateKey — RSA is required on the private-key side.
   openssl genrsa -traditional -out v2.priv.pem 2048
   openssl rsa  -in v2.priv.pem -pubout -out v2.pub.pem
   ```

2. Distribute `v2.pub.pem` to the writer's `PULSAR_ENCRYPTION_KEY_DIR` and
   `v2.priv.pem` to the deliverer's `PULSAR_ENCRYPTION_KEY_DIR`.

3. **Overlap window**: set the writer's `PULSAR_ENCRYPTION_KEY_NAMES=v1,v2`. New
   messages are now encrypted to both keys; consumers still holding only `v1` can
   decrypt them.

4. Roll the deliverer to a build/deploy that has `v2.priv.pem` present.

5. Drain the topic of `v1`-only messages by watching backlog metrics.

6. Set the writer's `PULSAR_ENCRYPTION_KEY_NAMES=v2`. Retire `v1.pub.pem` /
   `v1.priv.pem` from disk.

The multi-key encrypt path is required by FR-017 and is a first-class feature of
`pulsar-client-go`'s encryption API.

---

## Running the tests

### Unit only (fast)

```bash
go test ./...
```

### Integration (requires Docker)

```bash
go test -tags=integration ./...
```

This spins up a Pulsar testcontainer for `internal/pulsarlib/` and an `httptest`
stand-in for Pushover in `internal/deliverer/provider/pushover/`. Per the
constitution's Principle IV, **all code that talks to Pulsar or to Pushover has an
integration test**; mocks alone are not sufficient.

### Protobuf compatibility test (SC-007, FR-013)

A dedicated test in `pkg/notificationpb/v1/` marshals with the current-tree types and
unmarshals with the frozen copy under `testdata/notificationpb_prev/`, both directions.
Run as part of the default `go test` invocation; requires no extra flags.

---

## What a "green" submission looks like (acceptance walkthrough)

For User Story 1 (P1), a successful end-to-end run emits:

1. Writer structured log: `event=notification.accepted notification_id=... request_id=... originating_service=...`
2. Pulsar broker sees a new ciphertext message with a plaintext property `notification_id=...`.
3. Deliverer structured log: `event=notification.consumed notification_id=...`
4. Deliverer outcome log: `event=notification.outcome notification_id=... status=delivered`
5. A push lands on the Pushover client associated with `userOrGroupKey`.

All five records share the same `notification_id`, satisfying SC-005.
