# Secrets architecture and deployment reference

This page answers the question "where does each secret live, and who owns
it?" for people deploying the pulsar-notifcation-pipeline into a real
environment (not just the local `just up` stack).

## The three secrets in this system

```text
┌─────────────────────────┐        ┌─────────────────────────┐        ┌─────────────────────────┐
│  Upstream IoT / app     │        │   Writer  (API → Pulsar) │        │  Reader / Deliverer     │
│  (e.g. fan-notifier)    │        │                          │        │  (Pulsar → Pushover)    │
│                         │        │                          │        │                         │
│  ① Writer bearer token  │──HTTP─▶│  ① Tokens file            │  Pulsar  │  ③ Pushover app token  │
│  ② Pushover user key    │        │  ③ —                     │────────▶│  ② —                    │
│    (in request body)    │        │  ④ Public encryption key │         │  ④ Private decryption  │
│  ③ —                    │        │                          │        │       key               │
│  ④ —                    │        │                          │        │                         │
└─────────────────────────┘        └─────────────────────────┘        └─────────────────────────┘
                                                                                    │
                                                                                    ▼
                                                                          ┌──────────────────┐
                                                                          │ api.pushover.net │
                                                                          └──────────────────┘
```

| # | Secret | Purpose | Who holds it | Scope |
|---|--------|---------|--------------|-------|
| ① | **Writer bearer token** | Authenticates an upstream service to the writer's HTTP API | Each upstream app (as a secret); writer (as an entry in `WRITER_BEARER_TOKENS_FILE`) | One per upstream service |
| ② | **Pushover user/group key** | Identifies the recipient on Pushover's side | Upstream app, sent on every request body | One per recipient (could be per-user, or a group key for "everyone in the household") |
| ③ | **Pushover application API token** | Authenticates the pipeline *as a sender app* to Pushover's API | Deliverer (`PUSHOVER_APP_TOKEN`) | One per pipeline |
| ④ | **Pulsar E2EE key pair** | Encrypts notification payloads end-to-end between writer and deliverer; Pulsar brokers never see plaintext | Writer holds public key; deliverer holds private key | One (or more, during rotation) per pipeline |

## Walkthrough: an IoT fan-notifier

You have a small IoT app that fires a notification when the fan auto-starts.
Here's who holds what:

### The IoT app (`fan-notifier`)

Needs exactly two secrets, both app-scoped:

- **Writer bearer token** — issued once by whoever operates the pipeline and
  baked into the app's deployment secrets (env var, k8s secret, etc.). Used
  in `Authorization: Bearer <token>` on every submit.
- **Pushover user key** — the recipient. For your personal fan notifier,
  this is your own 30-character key from https://pushover.net. For a
  multi-person household, use a Pushover group key instead.

The app knows NOTHING about Pushover's API, the pipeline's encryption keys,
or the Pulsar substrate.

```go
// fan-notifier/main.go
c, _ := notificationclient.New(
    os.Getenv("PIPELINE_BASE_URL"),
    notificationclient.WithBearerToken(os.Getenv("PIPELINE_BEARER_TOKEN")),
)

func onFanOn() {
    _, _ = c.Submit(ctx, notificationclient.NewPushoverRequest(
        os.Getenv("PUSHOVER_USER_KEY"),
        "Fan on",
        "Automatic trigger hit at 72°F",
    ))
}
```

### The writer (`cmd/writer`)

- `WRITER_BEARER_TOKENS_FILE` → points at a file with one line per upstream
  service: `<token> <service-name>`. Each upstream service gets its own
  token so you can revoke `fan-notifier`'s access without breaking
  `doorbell-notifier`.
- `PULSAR_SERVICE_URL`, `PULSAR_TOPIC`, `PULSAR_ENCRYPTION_KEY_DIR`,
  `PULSAR_ENCRYPTION_KEY_NAMES` → connection + active **public** keys.

The writer never touches Pushover. It doesn't know Pushover exists.

### The deliverer (`cmd/deliverer`)

- `PUSHOVER_APP_TOKEN` → the single Pushover *application* credential,
  obtained once at https://pushover.net/apps/build. This identifies the
  pipeline as a sender to Pushover; ALL messages flow through this single
  app token regardless of which upstream service originated them.
- `PULSAR_SERVICE_URL`, `PULSAR_TOPIC`, `PULSAR_SUBSCRIPTION`,
  `PULSAR_ENCRYPTION_KEY_DIR` → connection + access to **private** keys.

The deliverer never sees any upstream's bearer token — it only consumes
already-authenticated-and-accepted messages from Pulsar.

## Why this split

1. **One Pushover app token centralises the sender identity.** Your fan
   app, doorbell app, and thermostat app all route through the same
   `PUSHOVER_APP_TOKEN` — they all appear as "My Home Pipeline" on every
   recipient device. Revoking this one token shuts down the pipeline
   instantly without touching any upstream app.
2. **User keys travel with messages** (FR-008, stateless routing) so one
   pipeline can deliver to `you`, `your-spouse`, or `household-group-key`
   on a per-event basis. The pipeline keeps no device registry.
3. **Per-upstream bearer tokens** let you revoke one misbehaving app
   (`fan-notifier` spamming? kill its token, leave the others) without
   affecting other upstreams.
4. **Encryption key pair is per-pipeline** — not per-upstream, not
   per-provider. A new upstream service joining the pipeline doesn't
   require new encryption keys; it just needs a bearer token.

## Where these secrets live in each deployment target

### Local (`just up` + docker-compose)

| Secret | Lives at |
|--------|----------|
| ① Writer bearer tokens | `.secrets/writer-tokens` (generated by `just dev-secrets`) |
| ② Pushover user key | `.env` `PUSHOVER_USER_KEY` (consumed by `just demo`); in production, the upstream app holds it |
| ③ Pushover app token | `.env` `PUSHOVER_APP_TOKEN` (loaded into the deliverer container) |
| ④ Encryption keys | `.secrets/pulsar-keys/v1.{pub,priv}.pem` |

The `.secrets/` and `.env` paths are gitignored. This setup is NOT a
production-shaped secret store.

### Kubernetes

Each secret maps to a `Secret` object mounted into the right workload:

```yaml
# Writer — holds the public key + the authoritative bearer-tokens file
apiVersion: v1
kind: Secret
metadata: { name: writer-secrets }
type: Opaque
stringData:
  tokens: |
    abc123 fan-notifier
    def456 doorbell-notifier
    ghi789 thermostat-notifier
  v1.pub.pem: |
    -----BEGIN PUBLIC KEY-----
    ...

# Deliverer — holds the private key + the Pushover app token
apiVersion: v1
kind: Secret
metadata: { name: deliverer-secrets }
type: Opaque
stringData:
  pushover-app-token: uxxxxxxxxxxxxxxxxxxxxxxxxxxx
  v1.priv.pem: |
    -----BEGIN RSA PRIVATE KEY-----
    ...

# fan-notifier — holds ONLY the bearer token + recipient user key
apiVersion: v1
kind: Secret
metadata: { name: fan-notifier-secrets }
type: Opaque
stringData:
  pipeline-bearer-token: abc123
  pushover-user-key: uxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

Mount the writer/deliverer secrets as files (the apps read them from paths
referenced by env vars). Mount the IoT app secret as env vars (simpler for
small apps).

### Secret managers (Vault, AWS Secrets Manager, GCP Secret Manager)

Same layout as Kubernetes — the manager is just the source of truth. A
sidecar or init container fetches values and writes them to the expected
paths. The pipeline's own code doesn't change; only the injection
mechanism does.

### Bare metal / systemd

- Writer: `/etc/writer/tokens`, `/etc/writer/keys/v1.pub.pem`, env vars in
  `/etc/writer/env` loaded via `EnvironmentFile=` in the systemd unit.
- Deliverer: same pattern, with `/etc/deliverer/keys/v1.priv.pem` and the
  `PUSHOVER_APP_TOKEN` in `/etc/deliverer/env` (mode 0600, root-readable
  only).

## Operator checklists

### Onboarding a new upstream service

1. Mint a random bearer token (e.g., `openssl rand -hex 24`).
2. Append `<token> <service-name>` to the writer's tokens file (or whatever
   store backs it in your environment).
3. Restart the writer (it loads tokens at startup; no hot-reload at MVP).
4. Give the token to the new upstream service via its own secret store.
5. The upstream service ALSO needs to know which Pushover user/group key to
   target — that's a per-upstream config choice, NOT issued by the pipeline
   operator. The operator doesn't see recipient identities.

### Revoking an upstream service

1. Remove its line from the writer's tokens file.
2. Restart the writer. Future submits from that service return
   `authentication-failed`.

### Rotating the Pulsar encryption keys

See [docs/key-rotation.md](./key-rotation.md). Zero-downtime via
multi-key overlap window (FR-017).

### Rotating the Pushover app token

1. Generate a new app token in the Pushover dashboard.
2. Update `PUSHOVER_APP_TOKEN` on the deliverer. Rolling restart.
3. All in-flight notifications awaiting dispatch will use whichever token
   is in the deliverer at dispatch time — no message-level dependency.
4. Revoke the old token in Pushover.

### Rotating an upstream service's bearer token

1. Issue a new token to the service alongside the old (two entries in the
   tokens file).
2. Roll the upstream service to use the new token.
3. Remove the old line from the tokens file.
4. Restart the writer.

## What to NEVER put where

- **Never put `PUSHOVER_APP_TOKEN` in an upstream app.** It's the
  pipeline's infrastructure credential; upstream apps MUST NOT need it to
  operate.
- **Never put a writer bearer token on the deliverer.** The deliverer is
  downstream of authentication — it trusts that anything on the Pulsar
  topic has already been authenticated and validated.
- **Never commit `.env`, `.secrets/`, or any `*.pem` file.** Both are
  gitignored; confirm before any `git add`.
- **Never ship Pushover user keys in firmware.** They change over time
  (people leave households, phones get replaced, keys get rotated) and
  re-flashing is costly. Load them at runtime from app config.
