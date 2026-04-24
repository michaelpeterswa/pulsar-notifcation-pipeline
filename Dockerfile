# ── Stage 1: build ──────────────────────────────────────────────────────────
# Dependencies are vendored before the Docker build (`just vendor`) so the
# builder has no outbound network access requirements.
FROM golang:1.25 AS builder

ARG TARGETARCH

WORKDIR /app

COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/writer    ./cmd/writer && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/deliverer ./cmd/deliverer

# ── Stage 2: writer ─────────────────────────────────────────────────────────
# hadolint ignore=DL3007
FROM gcr.io/distroless/static-debian12:latest AS writer
COPY --from=builder /out/writer /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]

# ── Stage 3: deliverer ──────────────────────────────────────────────────────
# hadolint ignore=DL3007
FROM gcr.io/distroless/static-debian12:latest AS deliverer
COPY --from=builder /out/deliverer /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
