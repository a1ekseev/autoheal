# syntax=docker/dockerfile:1.18

ARG GO_VERSION=1.27.2

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

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS testtarget
COPY --from=testtarget-build /out/target /target
EXPOSE 8080
ENTRYPOINT ["/target"]

# -------------------------------------------------------------- runtime ----
# Runs as root on purpose: it needs the Docker socket (root:docker 0660) and
# raw ICMP as a fallback, exactly like the original Python image. To run as
# non-root use `--user 65532 --group-add <docker gid>`.
# Settings and their defaults: see README (CHECK_INTERVAL, FAIL_THRESHOLD, ...).
FROM gcr.io/distroless/static-debian13:latest@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0 AS runtime
LABEL org.opencontainers.image.title="autoheal" \
      org.opencontainers.image.description="Docker container auto-heal monitor" \
      org.opencontainers.image.source="https://github.com/a1ekseev/autoheal"
COPY --from=build /out/autoheal /autoheal
ENTRYPOINT ["/autoheal"]
