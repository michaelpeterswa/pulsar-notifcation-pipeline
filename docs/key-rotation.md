# Operator runbook: rotating the Pulsar E2EE key pair

> See also: [docs/secrets.md](./secrets.md) for where every secret in the
> pipeline lives, who holds it, and the other rotation procedures (Pushover
> app token, upstream bearer tokens).

This is the zero-downtime procedure for rotating the asymmetric key pair that
protects notifications on the Pulsar topic. It implements FR-017's overlap-window
model: during rotation the writer encrypts each outgoing message to BOTH the
outgoing and incoming public keys, so in-flight messages already encrypted under
the outgoing key are still decryptable by consumers that only hold its
private key, and new consumers can start decrypting under the new key
immediately.

> **Key algorithm**: Pulsar Go client's default message crypto parses private
> keys via `x509.ParsePKCS1PrivateKey`, so private keys MUST be RSA in PKCS1
> format. Public keys are PKIX-encoded.

## Why rotate?

- Periodic hygiene (industry norm: ≤ 1 year per key).
- Suspected or confirmed compromise (emergency rotation — reduce the overlap
  window to zero; accept any in-flight messages under the old key may be
  undecryptable and recorded as `encryption-policy-violation`).

## Prerequisites

- Shell access to a workstation that can write to the key directories used by
  the writer (`PULSAR_ENCRYPTION_KEY_DIR`, typically a mounted Secret volume)
  and the deliverer (same variable on the deliverer side).
- The ability to restart the writer and the deliverer.
- Agreement on a new key name (e.g. `v2`). Names are first-class in the
  pipeline; they appear as plaintext on the Pulsar message (Pulsar's encrypted
  data-key reference), so choose non-sensitive short strings.

## Procedure — normal rotation (zero-downtime)

### 1. Generate the new key pair

```bash
openssl genrsa -traditional -out v2.priv.pem 2048
openssl rsa  -in v2.priv.pem -pubout -out v2.pub.pem
```

### 2. Distribute the new keys

- `v2.pub.pem` → the **writer**'s `PULSAR_ENCRYPTION_KEY_DIR`
- `v2.priv.pem` → the **reader/deliverer**'s `PULSAR_ENCRYPTION_KEY_DIR`

The deliverer needs the private key BEFORE the writer starts encrypting to `v2`.
If you reverse the order, messages encrypted to `v2` will hard-fail with
`encryption-policy-violation` (FR-016).

### 3. Roll the deliverer

Restart the deliverer (rolling restart across replicas). The `fileKeyReader`
resolves private keys by name at decrypt time, so the new `v2.priv.pem` is
picked up as soon as Pulsar asks for it.

**Verification**: watch for `deliverer.consumer.receive.error` log lines. Zero
is expected — the deliverer is not yet receiving messages encrypted to `v2`.

### 4. Open the overlap window on the writer

Update the writer's environment:

```
PULSAR_ENCRYPTION_KEY_NAMES=v1,v2
```

…and restart the writer (rolling). Every outgoing message is now encrypted to
BOTH `v1` and `v2`. Either key holder can decrypt.

**Verification**: on the deliverer logs, continue to see `status=delivered`.
On the Pulsar broker, new messages' internal encryption-keys list has two
entries.

### 5. Drain the topic of `v1`-only messages

Monitor the topic backlog for messages published before step 4. These are
encrypted only to `v1`, and the deliverer still decrypts them with the `v1`
private key. Wait until the backlog for the oldest `v1`-only publish time has
cleared.

A simple signal: use `pulsar-admin topics stats persistent://public/default/notifications`
and watch `backlogSize` trend to zero; then hold for a safety margin (30× normal
p99 consume latency — typically a few minutes).

### 6. Retire `v1` on the writer

Update the writer:

```
PULSAR_ENCRYPTION_KEY_NAMES=v2
```

…and restart (rolling). New messages are encrypted only to `v2`.

### 7. Remove `v1` from disk

Once no pending messages reference `v1`, remove the key files:

```bash
rm v1.pub.pem           # writer side
rm v1.priv.pem          # deliverer side
```

Done.

## Procedure — emergency rotation

If `v1` is known-compromised:

- Skip step 3's drain. Do steps 1, 2, 4, 6 (i.e., briefly overlap then cut over
  immediately).
- Accept that any `v1`-only messages in flight at the cut-over will surface as
  `encryption-policy-violation` terminal outcomes. Alert operators to replay
  those notifications via the writer API if the upstream service still has
  them.

## Rollback

If step 4 causes unexpected errors:

1. Revert the writer's `PULSAR_ENCRYPTION_KEY_NAMES` to `v1` (remove `v2`).
2. Restart the writer.
3. Investigate; consumers still have `v2.priv.pem` but are not receiving `v2`
   messages any more. Safe to leave `v2` files in place until the next
   rotation attempt.

## Verification checklist

- [ ] `openssl rsa -in v2.priv.pem -check` returns "RSA key ok"
- [ ] `openssl rsa -in v2.priv.pem -pubout` diff against `v2.pub.pem` is empty
- [ ] Writer logs show `encryption_key_names=[v1 v2]` during overlap
- [ ] Deliverer logs show zero `receive.error` during rotation
- [ ] Deliverer logs show zero `encryption-policy-violation` during rotation
  (normal mode) or a bounded, expected count (emergency mode)
- [ ] `v1` files removed from both sides after drain completes
