# pulsar-notifcation-pipeline — developer task runner
#
# Recipes are written for `just` (https://github.com/casey/just). On macOS:
# `brew install just`. On Linux: `cargo install just` or download a static
# binary from GitHub releases. Run `just` with no arguments for the list.

set shell := ["bash", "-euo", "pipefail", "-c"]

# Auto-load .env (gitignored) so recipes can read overrides like
# PUSHOVER_USER_KEY without the user having to export them manually.
# .env.example is the checked-in template.
set dotenv-load := true
set dotenv-required := false

module_path := "github.com/michaelpeterswa/pulsar-notifcation-pipeline"

# Show available recipes.
default:
    @just --list

# --- code generation -------------------------------------------------------

# Regenerate every contract artefact from its source of truth.
generate: proto openapi

# Regenerate protobuf Go code (both the canonical message and the frozen prev copy).
proto: proto-main proto-prev

proto-main: bin-protoc-gen-go
    PATH="{{justfile_directory()}}/bin:$PATH" protoc \
        --go_out=. --go_opt=module={{module_path}} \
        proto/notification/v1/notification.proto

proto-prev: bin-protoc-gen-go
    PATH="{{justfile_directory()}}/bin:$PATH" protoc \
        --go_out=. --go_opt=module={{module_path}} \
        proto/notification/v1/prev/notification.proto

# Local build of protoc-gen-go so protoc can invoke it from ./bin.
bin-protoc-gen-go:
    mkdir -p bin
    go build -o bin/protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go

# Regenerate OpenAPI-derived code (server stubs + public client library).
openapi: openapi-server openapi-client

openapi-server:
    go tool oapi-codegen --config=oapi-codegen.server.yaml api/openapi.yaml

openapi-client:
    go tool oapi-codegen --config=oapi-codegen.client.yaml api/openapi.yaml

# --- test ------------------------------------------------------------------

# Unit tests.
test:
    go test ./...

# Integration tests (requires Docker).
test-integration:
    go test -tags=integration -timeout=15m ./...

# Both test suites.
test-all: test test-integration

# --- lint ------------------------------------------------------------------

# All-in-one lint: funcoptslint (Principle II) + go vet + golangci-lint (if present).
lint: funcoptslint
    go vet ./...
    if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run ./...; \
    else \
        echo "golangci-lint not installed; skipping"; \
    fi

# Enforces Constitution Principle II (functional options).
funcoptslint:
    go run ./cmd/funcoptslint ./internal/... ./pkg/... ./cmd/writer ./cmd/deliverer

# --- build -----------------------------------------------------------------

build: build-writer build-deliverer

build-writer:
    go build -o bin/writer ./cmd/writer

build-deliverer:
    go build -o bin/deliverer ./cmd/deliverer

# Run from source.
run-writer:
    go run ./cmd/writer

run-deliverer:
    go run ./cmd/deliverer

# --- local stack -----------------------------------------------------------

# Generate dev-only secrets (RSA keys + bearer tokens) under .secrets/.
# Idempotent — re-running will not clobber existing material.
dev-secrets:
    ./scripts/dev-secrets.sh

# Bring up the local stack (Pulsar + writer + deliverer + LGTM).
# Ensures dev secrets exist first.
up: dev-secrets
    docker compose up --build

down:
    docker compose down

# Submit a demo notification through the running stack and assert a
# delivered outcome appears in the deliverer logs. Requires the stack to
# already be up (`just up`). Exits non-zero if the pipeline doesn't confirm
# delivery within 10 seconds.
#
# Recipient: first non-empty of (positional arg, $PUSHOVER_USER_KEY from the
# shell env, Pushover's docs example key). To auto-load from .env:
# `set -a; source .env; set +a; just demo`. For a real Pushover delivery
# set PUSHOVER_USER_KEY to YOUR user or group key.
#
#   just demo                                    # defaults / env
#   just demo 'ux3jQ...key' 'Hi' 'Body text'     # override recipient + message
demo user_key=env_var_or_default("PUSHOVER_USER_KEY", "uQiRzpo4DXghDmr9QzzfQu27cmVRsG") title="Demo from just" body="Sent via just demo":
    #!/usr/bin/env bash
    set -euo pipefail

    tokens_file=".secrets/writer-tokens"
    if [[ ! -f "$tokens_file" ]]; then
        echo "demo: $tokens_file not found; run 'just dev-secrets' first" >&2
        exit 1
    fi
    token=$(awk '!/^#/ && NF { print $1; exit }' "$tokens_file")
    if [[ -z "$token" ]]; then
        echo "demo: no bearer token in $tokens_file" >&2
        exit 1
    fi

    payload=$(TITLE={{quote(title)}} BODY={{quote(body)}} USER_KEY={{quote(user_key)}} \
        python3 -c 'import json,os; print(json.dumps({"content":{"title":os.environ["TITLE"],"body":os.environ["BODY"]},"pushover":{"userOrGroupKey":os.environ["USER_KEY"]}}))')

    echo "→ POST http://localhost:8080/notifications"
    resp=$(curl -sS --max-time 5 --fail-with-body -X POST http://localhost:8080/notifications \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -H "Idempotency-Key: just-demo-$RANDOM-$(date +%s)" \
        --data-binary "$payload") || {
        echo "demo: writer request failed (is 'just up' running?)" >&2
        exit 1
    }
    echo "← writer: $resp"
    nid=$(echo "$resp" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("data",{}).get("notification_id",""))' 2>/dev/null || echo "")
    if [[ -z "$nid" ]]; then
        echo "demo: writer response missing data.notification_id" >&2
        exit 1
    fi

    echo "• waiting for deliverer outcome (notification_id=$nid) ..."
    deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
        line=$(docker compose logs --no-color deliverer 2>/dev/null | grep "$nid" | grep 'notification.outcome' | tail -1 || true)
        if [[ -n "$line" ]]; then
            echo "← deliverer: $line"
            if echo "$line" | grep -q '"status":"delivered"'; then
                echo "demo: OK (notification delivered)"
                exit 0
            fi
            echo "demo: terminal outcome was not 'delivered' — see log line above" >&2
            exit 1
        fi
        sleep 0.5
    done
    echo "demo: no outcome recorded within 10s for $nid; recent deliverer logs:" >&2
    docker compose logs --no-color --tail=20 deliverer >&2
    exit 1

# Remove local build outputs.
clean:
    rm -rf bin/ dist/ coverage.out coverage.html

# --- housekeeping ----------------------------------------------------------

# One-time repo setup (pre-commit hooks, commitlint).
setup:
    npm install -g @commitlint/config-conventional @commitlint/cli
    git config --local core.hooksPath .githooks/

# Install git hooks only.
hooks:
    git config --local core.hooksPath .githooks/
