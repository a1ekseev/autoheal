# Task runner for autoheal — https://just.systems
# Run `just` to list recipes.

set shell := ["bash", "-euo", "pipefail", "-c"]

golangci_lint := env("GOLANGCI_LINT", "golangci-lint")
compose := "docker compose -f docker-compose.test.yml --profile '*'"

# List available recipes
default:
    @{{ just_executable() }} --list --unsorted

# Lint, unit tests and build — what CI checks before integration
all: lint test build

# Build the binary into bin/
build:
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/autoheal ./cmd/autoheal

# Unit tests with race detector and coverage (optionally pass packages)
test *packages="./...":
    go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out {{ packages }}

# Run unit tests and show the total coverage
cover: test
    go tool cover -func=coverage.out | tail -1

# go mod tidy check and golangci-lint (includes govet)
lint:
    go mod tidy -diff
    {{ golangci_lint }} run ./...

# Apply gofumpt / gci formatting
fmt:
    {{ golangci_lint }} fmt ./...

# Scan dependencies for known vulnerabilities
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

# Build the runtime image
image tag="autoheal:local":
    docker build --target runtime -t {{ tag }} .

# Integration tests: real autoheal container in docker compose
it: it-build
    AUTOHEAL_IT_BUILD=0 go test -tags integration -count=1 -v -timeout 15m ./integration/...

# Build the images used by the integration stand
it-build:
    {{ compose }} build

# Tear down the integration stand (after AUTOHEAL_IT_KEEP=1 runs)
it-down:
    {{ compose }} down -v --remove-orphans
