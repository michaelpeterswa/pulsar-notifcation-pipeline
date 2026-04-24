#!/usr/bin/env bash
#
# Generate dev-only secrets for the local docker-compose stack:
#
#   .secrets/pulsar-keys/v1.pub.pem   — RSA 2048 public key (mounted on writer)
#   .secrets/pulsar-keys/v1.priv.pem  — RSA 2048 private key (mounted on deliverer)
#   .secrets/writer-tokens            — bearer tokens (one <token> <service-name> per line)
#
# Idempotent: files already present are left untouched. Re-run after a full
# `rm -rf .secrets/` to rotate.
#
# NOT suitable for anything other than a local developer laptop. The tokens
# and keys are predictable across every developer by design. .secrets/ is
# gitignored; DO NOT commit it.

set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." >/dev/null && pwd)"
SECRETS_DIR="$REPO_ROOT/.secrets"
KEYS_DIR="$SECRETS_DIR/pulsar-keys"
TOKENS_FILE="$SECRETS_DIR/writer-tokens"

command -v openssl >/dev/null 2>&1 || {
    echo "dev-secrets: openssl is required but not on PATH" >&2
    exit 1
}

mkdir -p "$KEYS_DIR"
chmod 700 "$SECRETS_DIR" "$KEYS_DIR"

pub="$KEYS_DIR/v1.pub.pem"
priv="$KEYS_DIR/v1.priv.pem"

if [[ -f "$pub" && -f "$priv" ]]; then
    echo "dev-secrets: pulsar keys already present at $KEYS_DIR (skipping)"
else
    echo "dev-secrets: generating RSA 2048 key pair at $KEYS_DIR"
    # PKCS1 is required on the private-key side for the Pulsar Go client's
    # default message crypto (x509.ParsePKCS1PrivateKey). See
    # docs/key-rotation.md and specs/001-notification-pipeline/research.md §5.
    openssl genrsa -traditional -out "$priv" 2048 >/dev/null 2>&1
    openssl rsa -in "$priv" -pubout -out "$pub" >/dev/null 2>&1
    chmod 600 "$priv"
    chmod 644 "$pub"
fi

if [[ -f "$TOKENS_FILE" ]]; then
    echo "dev-secrets: bearer-tokens file already present at $TOKENS_FILE (skipping)"
else
    echo "dev-secrets: writing bearer-tokens file at $TOKENS_FILE"
    cat > "$TOKENS_FILE" <<'EOF'
# Dev-only bearer tokens. Format: <token> [service-name]
# This file is gitignored; regenerate with `just dev-secrets` after `rm -rf .secrets/`.
local-dev-token local-dev
EOF
    chmod 600 "$TOKENS_FILE"
fi

echo "dev-secrets: ready."
echo "  keys   : $KEYS_DIR"
echo "  tokens : $TOKENS_FILE"
echo
echo "Use 'Authorization: Bearer local-dev-token' when calling the writer."
