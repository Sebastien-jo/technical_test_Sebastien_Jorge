# Rate Limiter Service

[![PR Checks](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/pr-checks.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/pr-checks.yml)
[![Main Merge](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/main-merge.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/main-merge.yml)
[![Nightly Endurance](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/schedule-extended-tests.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/schedule-extended-tests.yml)

A production-ready HTTP rate limiting service built in Go with Redis storage and an in-memory fallback.

## Architecture

```
cmd/server/         — entrypoint, wires all layers
internal/
  config/           — Viper config loading, policy deserialization
  models/           — domain types (no framework dependencies)
  service/          — Token Bucket algorithm, policy matching
  storage/          — Redis + in-memory backends, hybrid fallback
  handler/          — HTTP handlers and middleware (Gin)
tests/              — integration tests + K6 load test scripts
scripts/            — helper shell scripts
```

## Endpoints

| Method | Path                    | Description                              |
|--------|-------------------------|------------------------------------------|
| POST   | /check                  | Check if a request is within quota       |
| GET    | /policies               | List all configured client IDs           |
| GET    | /policies/:client_id    | Inspect routes and limits for one client |
| GET    | /health                 | Storage health check                     |

### POST /check

```json
{
  "client_id":  "mobile_app",
  "route":      "/api/videos/123",
  "method":     "GET",
  "session_id": "user-session-abc",
  "ip":         "1.2.3.4",
  "user_agent": "Mozilla/5.0"
}
```

Response `200`:
```json
{ "allowed": true, "remaining": 99, "reset_time": 1700000060 }
```

Response `429`:
```json
{ "allowed": false, "remaining": 0, "retry_after": 12, "message": "rate limit exceeded" }
```

Rate-limit headers are set on every response:
- `X-RateLimit-Limit` — configured limit for the matched route
- `X-RateLimit-Remaining` — tokens left in the current window
- `X-RateLimit-Reset` — Unix timestamp when the bucket refills
- `Retry-After` — seconds to wait (429 only)

## Quick Start

```bash
docker compose up --build
```

The server starts on port `8080`. Redis starts automatically.

## Configuration

Settings are loaded from `config.yaml` (overridable via environment variables).

```yaml
server:
  port: "8080"
  timeout: 30s

redis:
  host: localhost
  port: 6379
  db: 0

policies:
  - client_id: mobile_app
    routes:
      - route: /api/videos
        route_type: prefix   # "exact" or "prefix"
        method: "*"          # HTTP method or "*" for all
        limit: 100
        window: 1m
        identifier: session_id  # "none" | "session_id" | "ip" | "ip_user_agent"
```

Environment variable override example: `REDIS_HOST=my-redis SERVER_PORT=9090`.

## Testing

**Prerequisites: Docker + Docker Compose only. Nothing else to install.**

### Option 1 — Make (recommended)

```bash
make test-integration   # Go integration tests         (~5s)
make test-load          # K6 normal load test          (~2min)
make test-spike         # K6 spike test                (~30s)
make test-endurance     # K6 endurance test            (~11min)
make test-all           # All of the above in sequence
make full-test          # up → test-all → down
```

### Option 2 — Bash script

```bash
chmod +x scripts/run-tests.sh
./scripts/run-tests.sh integration
./scripts/run-tests.sh load
./scripts/run-tests.sh all
```

### Option 3 — Docker Compose directly

```bash
# Integration tests (self-contained, no app needed)
docker compose --profile test run --rm test

# K6 load tests (starts app + redis automatically)
docker compose --profile load-test run --rm k6-load
docker compose --profile load-test run --rm k6-spike
docker compose --profile load-test run --rm k6-endurance
```

## Test Suites

### Integration tests (`tests/integration_test.go`)

Go end-to-end tests using `httptest` and in-memory storage — no live server required.

Covers:
- Basic allow / deny / retry-after flow
- Quota isolation: by client, by route, by identifier (session / IP)
- Route matching: exact, prefix, wildcard method, method case insensitivity
- Input validation: missing fields, unknown client, unknown route
- HTTP headers: `X-Request-ID`, `X-RateLimit-*`, `Retry-After`
- Policies endpoint: list all clients, inspect routes per client
- Health endpoint
- Exact quota exhaustion (11th request denied when limit=10)
- Concurrent requests: 150 goroutines → exactly 100 allowed, 50 denied
- Storage persistence across sequential requests
- Edge cases: burst, invalid JSON, method case fold

Expected: all tests pass in ~2–5 seconds.

### K6 load test (`tests/load_test.js`)

Ramp up 10→100 req/s over 30s, sustain for 1 min, ramp down over 10s.

Thresholds:
- `p(95) < 500ms`
- failure rate < 10%
- rate-limited requests < 50%

### K6 spike test (`tests/load_test_spike.js`)

10s at 50 req/s → 5s spike at 500 req/s → 10s recovery → ramp down.

Relaxed thresholds (`p(99) < 2s`, failure < 20%) to validate resilience under surge.

### K6 endurance test (`tests/load_test_endurance.js`)

Sustained 50 req/s for 10 minutes to surface memory leaks and latency drift.

Same strict thresholds as the normal load test (`p(95) < 500ms`).

## Expected Results

| Test suite   | Duration | Expected outcome                       |
|--------------|----------|----------------------------------------|
| Integration  | ~5s      | 100% pass                              |
| Load         | ~2min    | p95 < 500ms, <10% failures             |
| Spike        | ~30s     | p99 < 2s, no panics                    |
| Endurance    | ~11min   | Stable latency, no memory growth       |

## CI/CD Pipeline

### Workflows

| Workflow | Trigger | Jobs |
|----------|---------|------|
| **PR Checks** | Every pull request to `main` | lint, unit tests (race + coverage), integration tests, load + spike tests, gosec scan |
| **Main Merge** | Push to `main` | All PR checks → Docker build → Trivy container scan → deployment smoke test |
| **Nightly Endurance** | `cron: 0 2 * * *` (manual via `workflow_dispatch`) | K6 endurance test (~11 min) |

### PR Checks detail

All five jobs run in **parallel**:

- **Lint** — `golangci-lint` with `errcheck`, `govet`, `staticcheck`, `gosec`, `gocritic`, and more
- **Unit Tests** — `go test -race` across `./internal/...` with 80% coverage threshold enforced
- **Integration Tests** — Go end-to-end tests in Docker via `make test-integration`
- **Load Tests** — K6 normal load (~2 min) + spike (~30 s) via Docker Compose
- **Security Scan** — `gosec` static analysis for common Go security issues

### Main merge additions

After all PR checks pass:

- Docker image is built and exported as a workflow artifact
- **Trivy** scans the image for `CRITICAL` and `HIGH` vulnerabilities — fails the build if any are found
- A smoke test starts the full stack and exercises every endpoint

### Running CI checks locally

```bash
make ci-lint          # golangci-lint
make ci-unit          # unit tests with -race + coverage report
make ci-integration   # integration tests in Docker
make ci-load          # build image, then load + spike tests
make ci-all           # everything above in sequence
```

### Dependency updates

[Dependabot](.github/dependabot.yml) opens weekly PRs to update Go modules and GitHub Actions pins. PRs are labelled `dependencies` and respect the standard PR check gate.

## Troubleshooting

**Port 8080 already in use**
```bash
make down   # or: docker compose down
```

**K6 cannot reach the app**
The K6 containers communicate via the `rate-limiter-net` bridge network using the hostname `app`. Make sure the app is running and healthy:
```bash
docker compose ps
make health
```

**Go test module download is slow on first run**
The `go-cache` Docker volume caches downloaded modules. Subsequent runs are fast.

**Redis not available at startup**
The app automatically falls back to in-memory storage and retries Redis reconnection every 10 seconds.
