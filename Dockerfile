# Multi-stage build for the pulsar-notifcation-pipeline.
# Parameterised by the APP build-arg: writer | deliverer.
#
# Build a specific binary:
#   docker build --build-arg APP=writer    -t pulsar-np-writer    .
#   docker build --build-arg APP=deliverer -t pulsar-np-deliverer .
#
# Both final images also ship `/healthcheck` — a tiny static HTTP probe used
# by docker-compose's healthcheck block (the distroless base has no shell).

# -=-=-=-=-=-=- Compile Image -=-=-=-=-=-=-
FROM golang:1.25 AS stage-compile

ARG APP=writer

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# hadolint ignore=DL3062
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/app ./cmd/${APP} && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/healthcheck ./cmd/healthcheck

# -=-=-=-=- Final Distroless Image -=-=-=-=-

# hadolint ignore=DL3007
FROM gcr.io/distroless/static-debian12:latest AS stage-final

COPY --from=stage-compile /out/app /app
COPY --from=stage-compile /out/healthcheck /healthcheck
USER nonroot:nonroot
ENTRYPOINT ["/app"]
