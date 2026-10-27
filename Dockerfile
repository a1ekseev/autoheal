# syntax=docker/dockerfile:1.28

ARG GO_VERSION=1.27.3

# ----------------------------------------------------------------- deps ----
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS deps
WORKDIR /src
# Optional extra CA bundle for TLS-intercepting proxies:
#   docker build --secret id=ca,src=/path/to/ca.crt .
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=secret,id=ca,required=false \
    if [ -f /run/secrets/ca ]; then export SSL_CERT_FILE=/run/secrets/ca; fi; \
    go mod download
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH

# ---------------------------------------------------------------- build ----
FROM deps AS build
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/autoheal ./cmd/autoheal

# ---------------------------------------------- integration test target ----
FROM deps AS testtarget-build
COPY test/target ./test/target
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/target ./test/target

FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS testtarget
COPY --from=testtarget-build /out/target /target
EXPOSE 8080
ENTRYPOINT ["/target"]

# -------------------------------------------------------------- runtime ----
# Runs as nonroot (65532). The Docker socket is root:docker 0660, so the
# container needs `--group-add <gid of the socket>`. Ping uses unprivileged ICMP
# sockets, allowed in containers by Docker 20.10+ (net.ipv4.ping_group_range).
# Settings and their defaults: see README (CHECK_INTERVAL, FAIL_THRESHOLD, ...).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS runtime
LABEL org.opencontainers.image.title="autoheal" \
      org.opencontainers.image.description="Docker container auto-heal monitor" \
      org.opencontainers.image.source="https://github.com/a1ekseev/autoheal"
COPY --from=build /out/autoheal /autoheal
ENTRYPOINT ["/autoheal"]
