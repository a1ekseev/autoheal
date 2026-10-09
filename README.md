# autoheal

Docker container auto-heal monitor, written in Go (port of the original
Python `build/app/monitor.py`, removed after the port; see commit
[`ab78041`](https://github.com/a1ekseev/autoheal/tree/ab78041/build)).

Every `CHECK_INTERVAL` seconds it lists **running** containers, checks those
labelled `autoheal.monitor.enable=true` with an ICMP ping or an HTTP GET, and
restarts a container once it has failed `FAIL_THRESHOLD` checks in a row.

## Usage

```yaml
services:
  autoheal:
    image: ghcr.io/a1ekseev/autoheal:master
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      CHECK_INTERVAL: "10"

  web:
    image: nginx
    labels:
      autoheal.monitor.enable: "true"
      autoheal.monitor.curl: http://web/health
      autoheal.monitor.curl.response: OK
```

The monitor must share a network with the containers it checks (or use the
host network) and needs access to the Docker socket.

### Labels

| Label | Meaning |
|---|---|
| `autoheal.monitor.enable` | `true` (case-insensitive, not trimmed) enables monitoring |
| `autoheal.monitor.ping` | host to ping (IPv4 ICMP echo); takes priority over `curl` |
| `autoheal.monitor.curl` | URL to GET; redirects are not followed, the status code is ignored |
| `autoheal.monitor.curl.response` | substring the body must contain (default: empty = any response) |

An enabled container without `ping`/`curl` is listed in the log but never restarted.

### Environment

| Variable | Default | Validation |
|---|---|---|
| `CHECK_INTERVAL` | `10` | integer seconds, `1..86400` |
| `FAIL_THRESHOLD` | `3` | integer, `>= 1` |
| `PING_TIMEOUT` | `10` | seconds (float), `0 < x <= 86400` |
| `CURL_TIMEOUT` | `10` | seconds (float), `0 < x <= 86400`, total request deadline |
| `LOG_LEVEL` | `INFO` | `DEBUG`/`NOTSET`, `INFO`, `WARN(ING)`, `ERROR`, `CRITICAL`/`FATAL` (case-insensitive) |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | standard Docker client variables |

Invalid values stop the monitor at start-up with exit code 1 and a message
listing every problem. An empty variable means "use the default".

## Differences from the Python version

Labels, settings and log lines are kept identical. Deliberate changes:

1. A ping that fails with a DNS / unreachable error counts as a failure
   (Python logged `[PING OK] ... 0.00 ms` and reset the counter).
2. If the monitor itself cannot open ICMP sockets (no `CAP_NET_RAW` and no
   `net.ipv4.ping_group_range`), it logs `[ERROR] Cannot ping ...` and does
   **not** count it against the container (Python crashed). A check
   interrupted by shutdown is not counted either.
3. Docker API errors are logged (`[ERROR] Failed to list containers: ...`) and
   the next cycle runs; Python crashed. `DOCKER_HOST` is honoured.
4. Configuration is validated up front (see table): numbers must be plain
   (`" 5"`, `"1_0"` are rejected), an empty variable means the default.
5. SIGTERM/SIGINT stop the monitor immediately with exit code 0.
6. Failure counters of containers that are gone are dropped.
7. HTTP check: `CURL_TIMEOUT` is a total deadline (httpx applied it per
   phase); only the first 1 MiB of the body is searched; User-Agent is
   `autoheal`. Like httpx it uses HTTP/1.1 only and does not follow redirects.
8. Requires Docker Engine API >= 1.40 (Docker 19.03+).

## Development

Requires Go 1.27+, Docker with Compose v2, golangci-lint v2 and
[just](https://just.systems) (`brew install just`, `cargo install just`, …).

```sh
just              # list recipes
just lint         # go mod tidy -diff, golangci-lint
just test         # unit tests (race, coverage); `just test ./internal/monitor/`
just cover        # unit tests + total coverage
just it           # integration tests: real autoheal container in docker compose
just image        # build the runtime image
```

`GOLANGCI_LINT=/path/to/golangci-lint just lint` overrides the linter binary.

The integration stand (`docker-compose.test.yml`) runs the real `autoheal`
image against real target containers on a real Docker daemon:

- restarted: wrong body, refused connection, redirect (not followed), slow
  response, unknown host, no ICMP reply - after exactly `FAIL_THRESHOLD`
  failures, with the counter reset after each restart;
- left alone: healthy, 500 with the expected body, any response without an
  expectation, flapping (reset on success), ping-over-curl, no check,
  `enable` false / " true" / missing, not running;
- a monitor without ICMP permission (`cap_drop: NET_RAW`) restarts nothing
  for ping, `LOG_LEVEL=WARNING` hides INFO;
- `DOCKER_HOST` through a socket proxy that forbids POST: restart fails, is
  logged and retried every cycle;
- Docker unreachable: errors logged, process keeps running;
- log format, startup line, cycle timing, SIGTERM exit 0, invalid config exit 1.

CI (`.github/workflows/ci.yml`): lint (golangci-lint, hadolint) → unit tests →
govulncheck → integration tests in compose → multi-arch image pushed to
`ghcr.io/a1ekseev/autoheal` on `master` and `v*` tags.
