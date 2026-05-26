# Rate Limiter Service

[![PR Checks](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/pr-checks.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/pr-checks.yml)
[![Main Merge](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/main-merge.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/main-merge.yml)
[![Nightly Endurance](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/schedule-extended-tests.yml/badge.svg)](https://github.com/sebastien-jorge/rate-limiter/actions/workflows/schedule-extended-tests.yml)

A production-ready HTTP rate limiting service built in Go. It enforces per-client, per-route quotas using a **Token Bucket** algorithm, with Redis as the source of truth via an atomic Lua script and an **in-memory fallback** that activates automatically when Redis is unavailable.

---

## Table of Contents

- [Architecture](#architecture)
- [Algorithm](#algorithm)
- [Storage](#storage)
- [Observability](#observability)
- [API Reference](#api-reference)
  - [POST /check](#post-check)
  - [GET /policies](#get-policies)
  - [GET /policies/:client\_id](#get-policiesclient_id)
  - [GET /health](#get-health)
- [Common Headers](#common-headers)
- [Configuration](#configuration)
- [Quick Start](#quick-start)
- [Testing](#testing)
- [CI/CD Pipeline](#cicd-pipeline)
- [Troubleshooting](#troubleshooting)

---

## Architecture

The service is structured in strict layers. Each layer depends only on the layers below it; no upward dependencies.

```
cmd/                     entry point — pure wiring, no business logic
└── internal/
    ├── config/          loads YAML + env vars into models.* types
    ├── models/          pure domain types (no framework tags)
    ├── storage/         Store interface + Redis / Memory / Hybrid implementations
    ├── service/         RateLimiter + PolicyMatcher (business logic)
    ├── handler/         Gin HTTP handlers, DTOs, middleware
    └── observability/   structured logging, DogStatsD metrics, DataDog APM
```

- **Handler** — parses HTTP requests, validates input, writes responses, sets headers. No rate-limiting logic.
- **Service** — `RateLimiter.Check()` resolves a request to a bucket key and delegates the atomic check+consume to the storage layer. `PolicyMatcher.FindPolicy()` resolves the active policy (exact match wins, then longest prefix).
- **Storage** — three-method `Store` interface: `CheckTokenBucket`, `Health`, `Clear`. The hot path runs atomically — Redis via a Lua script, in-memory via a mutex-guarded map.
- **Models** — domain types (`RoutePolicy`, `Decision`, `RedisConfig`, `QuotaKey`) with no framework tags.
- **Config** — Viper-backed loader that produces `models.*` types. `cmd/main.go` contains zero Viper calls; the config package is the single boundary between external configuration sources and the rest of the app.

### Request Flow

```
HTTP Request
    │
    ▼
Middleware (RequestID, Logger, Metrics)
    │
    ▼
Handler.Check  (parse + validate JSON body)
    │
    ▼
PolicyMatcher.FindPolicy(client_id, route, method)
    │   ├─ exact match (returns on first hit)
    │   └─ longest prefix match (path-boundary aware)
    │
    ▼
RateLimiter.Check(ctx, client_id, route_policy, identifier)
    │   build key = rl:{client_id}:{method}:{route}:{identifier}
    │   refillRate = limit / window.Seconds()
    │   ttl        = 2 × window
    │
    ▼
Store.CheckTokenBucket(ctx, key, capacity, refillRate, ttl)
    │   ├─ Redis path:  EVAL Lua script (atomic refill + consume + persist)
    │   └─ Memory path: mutex-guarded refill + consume on local bucket
    │
    ▼
Response (200 allowed | 429 limited) + X-RateLimit-* headers
```

---

## Algorithm

The service uses a **Token Bucket** algorithm with continuous (smooth) refill — no fixed windows, no boundary bursts.

- Each bucket starts full at capacity `limit` and refills at `limit / window` tokens per second.
- A request consumes one token. If the bucket is empty, the request is denied and `retry_after` returns the time until one token is available.
- Bucket state lives in storage. The Go process holds no in-process counters — restarting the service does not reset quotas.

### Bucket key format

```
rl:{client_id}:{method}:{route}:{identifier}
```

Defined once in `models.QuotaKey.String()` and used identically by service and storage layers, so a Redis key written by one instance is read by every other instance.

### Atomicity guarantees

The "read state → compute refill → consume → write state" sequence is atomic in both backends:

- **Redis** — implemented as a Lua script (`checkBucketScript`) running server-side. Redis blocks all other commands while a script executes, so concurrent callers across N service instances cannot race against the same key. The script uses Redis-side `TIME` rather than the caller's clock to eliminate skew between instances.
- **Memory** — implemented under a `sync.Mutex` on the bucket map. Concurrent goroutines on the same key serialise correctly.

This matters because rate limiting only works if "1 + 1" never becomes "2 allowed" when the limit is 1. Without atomicity, two instances could each read `tokens=1`, both decide "allowed", and both write `tokens=0` — overshooting the configured limit by 2×.

### TTL and cleanup

Each bucket key carries a TTL of `2 × window` (e.g. a 1-minute policy → 2-minute TTL). Idle buckets are reclaimed automatically:

- **Redis** evicts the hash via `PEXPIRE` when no further checks refresh it.
- **Memory** has a cleanup goroutine that scans and removes expired entries every minute.

This bounds memory growth at "active clients" rather than "total clients ever seen."

---

## Storage

The `Store` interface is intentionally narrow — three methods covering the only operations the service actually needs:

```go
type Store interface {
    CheckTokenBucket(ctx, key, capacity, refillRate, ttl) (BucketResult, error)
    Health(ctx) error
    Clear(ctx) error
}
```

Three implementations satisfy it:

| Implementation | Atomicity | Where state lives | Use |
|---|---|---|---|
| `RedisStore` | Server-side Lua | Redis hash per bucket key | Production / multi-instance |
| `MemoryStore` | `sync.Mutex` | Go map in-process | Tests, fallback target |
| `HybridStore` | Delegates | Redis primary, memory fallback | The wired-up default |

### What Redis stores

Each bucket is a Redis hash:

```
HKEY: rl:{client_id}:{method}:{route}:{identifier}
  tokens          -> "1842.5"        (float, stringified for precision)
  last_refill_ms  -> "1735632000123" (Redis-side TIME at last update)
TTL: 2 × policy.window
```

The Lua script reads both fields, computes refill, decrements, writes the new state, and resets the TTL — all in a single atomic operation per request.

### Hybrid failover

`HybridStore` wraps `RedisStore` and `MemoryStore`. Failover is invisible to the service layer:

1. **Normal operation** — all reads/writes go to Redis; the memory store is idle.
2. **Redis failure** — first error trips `onRedisFailure` (CAS-guarded so only one transition fires), the store flips to memory mode, and a reconnect goroutine starts ticking every 10s. The failing request is served from memory rather than failing — no 5xx is returned.
3. **Redis recovery** — the reconnect goroutine sees a healthy ping, swaps in the new client, and flips back to Redis mode. The transition is atomic from the caller's perspective.

### Trade-offs during fallback

- **Per-instance limiting** — each replica enforces the limit against its own in-memory map. With N replicas, effective throughput can briefly reach N × the configured limit during an outage. Acceptable for soft API quotas; not appropriate for billing-grade enforcement.
- **No state migration on transition** — in-memory state is silently abandoned when Redis comes back. Token bucket's continuous refill mostly hides this (the bucket would have refilled during the outage anyway), but for short outages with heavy bursts there can be measurable over-allowance.
- **Fail-open on storage error** — if `CheckTokenBucket` itself returns an error (rare — only when both Redis and memory fail), the service logs `ERROR` and allows the request rather than rejecting it. The rate limiter must not become a single point of failure for the API it protects.

---

## Observability

The service is instrumented across three pillars. All three are **no-op by default** and activate only when the relevant environment variables are set, so the service starts cleanly with no external dependencies.

### Structured logging

All log output is JSON, written to stdout. Every log line is a flat object with consistent fields:

```json
{
  "time": "2026-04-29T18:00:00Z",
  "level": "INFO",
  "msg": "request",
  "trace_id": "a1b2c3d4-...",
  "method": "POST",
  "path": "/check",
  "status": 200,
  "latency_ms": 3,
  "dd.trace_id": "7234103429384",
  "dd.span_id":  "2456731823"
}
```

`dd.trace_id` and `dd.span_id` are present when an active Datadog APM span exists, which is what Datadog uses to link a log line directly to its trace in the UI.

### Metrics (DogStatsD)

Metrics are sent to a DogStatsD agent when `DD_AGENT_HOST` is set. All metric names are prefixed with `rate_limiter.`.

| Metric | Type | Tags | Description |
|---|---|---|---|
| `rate_limiter.http.requests` | counter | `endpoint`, `status` | Total HTTP requests per endpoint and status code |
| `rate_limiter.http.latency_ms` | histogram | `endpoint`, `status` | Request latency in milliseconds |
| `rate_limiter.decision.allowed` | counter | `client_id`, `route` | Requests allowed by the rate limiter |
| `rate_limiter.decision.denied` | counter | `client_id`, `route` | Requests rejected by the rate limiter |

### APM traces (Datadog)

When `DD_AGENT_HOST` is set, the service starts a Datadog APM tracer and wraps the Gin router with `dd-trace-go`. Every HTTP request gets a span with the route, method, and status code. Additional environment variables:

| Variable | Description | Example |
|---|---|---|
| `DD_AGENT_HOST` | Enables both DogStatsD metrics and APM tracing | `datadog-agent` |
| `DD_ENV` | Deployment environment tag | `production` |
| `DD_VERSION` | Service version tag | `1.2.0` |

> **Provider note:** logging (`slog`) and metrics (behind the `Metrics` interface) are provider-agnostic and straightforward to swap. The APM tracer (`dd-trace-go`) is Datadog-specific; migrating to another provider would require replacing it with an OpenTelemetry SDK.

---

## API Reference

Base URL: `http://localhost:8080`

All requests and responses use `application/json`. Every response includes a `X-Trace-ID` header for distributed tracing.

---

### POST /check

Evaluates whether a request from a given client is within its configured quota for the matched route. This is the core endpoint that your API gateway or middleware calls on every inbound request.

#### Request

```
POST /check
Content-Type: application/json
```

| Field        | Type   | Required | Description                                                  |
|--------------|--------|----------|--------------------------------------------------------------|
| `client_id`  | string | **yes**  | Identifies the API client (must match a configured policy)   |
| `route`      | string | **yes**  | The request path (e.g. `/api/videos/123`)                    |
| `method`     | string | **yes**  | HTTP method of the original request (e.g. `GET`, `POST`)     |
| `session_id` | string | no       | Session token; used when the policy identifier is `session_id` |
| `ip`         | string | no       | Client IP; used when the policy identifier is `ip_user_agent`         |
| `user_agent` | string | no       | User-Agent string; used when the policy identifier is `ip_user_agent`  |

**Example request:**

```json
{
  "client_id":  "mobile_app",
  "route":      "/api/videos/123",
  "method":     "GET",
  "session_id": "user-session-abc",
  "ip":         "1.2.3.4",
  "user_agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)"
}
```

#### Responses

**`200 OK` — request is within quota**

```json
{
  "allowed":    true,
  "remaining":  99,
  "reset_time": 1700000060
}
```

| Field        | Type    | Description                                              |
|--------------|---------|----------------------------------------------------------|
| `allowed`    | boolean | Always `true` for 200 responses                          |
| `remaining`  | integer | Number of tokens remaining in the current bucket         |
| `reset_time` | integer | Unix timestamp (seconds) when the bucket will be full    |

---

**`429 Too Many Requests` — quota exceeded**

```json
{
  "allowed":     false,
  "remaining":   0,
  "reset_time":  1700000060,
  "retry_after": 12,
  "message":     "rate limit exceeded"
}
```

| Field         | Type    | Description                                              |
|---------------|---------|----------------------------------------------------------|
| `allowed`     | boolean | Always `false` for 429 responses                         |
| `remaining`   | integer | Always `0` when the quota is exhausted                   |
| `reset_time`  | integer | Unix timestamp (seconds) when the bucket will be full    |
| `retry_after` | integer | Seconds to wait before retrying                          |
| `message`     | string  | Human-readable reason                                    |

---

**`400 Bad Request` — missing or invalid fields**

```json
{ "error": "Key: 'CheckRequest.ClientID' Error:Field validation for 'ClientID' failed on the 'required' tag" }
```

**`404 Not Found` — unknown client or route**

```json
{ "error": "client not found" }
```

```json
{ "error": "route not found" }
```

**`500 Internal Server Error`**

```json
{ "error": "internal error" }
```

---

### GET /policies

Returns the list of all configured client IDs.

#### Request

```
GET /policies
```

No body, no parameters.

#### Response

**`200 OK`**

```json
{
  "clients": ["mobile_app", "web_app", "partner_api"]
}
```

| Field     | Type            | Description                      |
|-----------|-----------------|----------------------------------|
| `clients` | array of string | All client IDs with active policies |

---

### GET /policies/:client_id

Returns the full rate-limit configuration for a specific client, including every route rule.

#### Request

```
GET /policies/mobile_app
```

| Parameter   | Location | Description          |
|-------------|----------|----------------------|
| `client_id` | path     | The client to inspect |

#### Response

**`200 OK`**

```json
{
  "client_id": "mobile_app",
  "routes": [
    {
      "route":      "/api/videos",
      "route_type": "prefix",
      "method":     "*",
      "limit":      100,
      "window":     "1m0s",
      "identifier": "session_id"
    },
    {
      "route":      "/api/upload",
      "route_type": "exact",
      "method":     "POST",
      "limit":      10,
      "window":     "1h0m0s",
      "identifier": "ip_user_agent"
    }
  ]
}
```

| Field       | Type            | Description                                          |
|-------------|-----------------|------------------------------------------------------|
| `client_id` | string          | The client ID                                        |
| `routes`    | array of object | One entry per configured route rule (see table below) |

**Route object fields:**

| Field        | Type    | Description                                                       |
|--------------|---------|-------------------------------------------------------------------|
| `route`      | string  | The path pattern                                                  |
| `route_type` | string  | `"exact"` or `"prefix"` (see [Route Matching](#route-matching))   |
| `method`     | string  | HTTP method or `"*"` for any method                               |
| `limit`      | integer | Maximum number of requests allowed per window                     |
| `window`     | string  | Time window as a Go duration string (e.g. `"1m0s"`, `"1h0m0s"`)  |
| `identifier` | string  | Bucket scoping strategy (see [Identifier Types](#identifier-types)) |

**`404 Not Found`**

```json
{ "error": "client not found" }
```

---

### GET /health

Checks whether the service and its storage backend are healthy. Suitable for load-balancer health checks and liveness probes.

#### Request

```
GET /health
```

No body, no parameters.

#### Responses

**`200 OK` — service is healthy**

```json
{
  "status":  "ok",
  "storage": "ok"
}
```

**`503 Service Unavailable` — storage is degraded**

```json
{
  "status":  "degraded",
  "storage": "degraded"
}
```

> **Note:** A `degraded` storage response is returned when the Redis ping itself fails at the moment of the health check. When `HybridStore` has already flipped to memory fallback and the reconnect loop is running, the health endpoint currently reports `ok` since the service is still serving traffic correctly. Watch the structured logs for `[storage] Redis ... fallback` entries (or wire up a metric on `usingMem`) to alert on degraded mode while still using `/health` as a liveness probe.

| Field     | Type   | Values              | Description                   |
|-----------|--------|---------------------|-------------------------------|
| `status`  | string | `"ok"`, `"degraded"` | Overall service health        |
| `storage` | string | `"ok"`, `"degraded"` | Storage backend health        |

---

## Common Headers

### Request Headers

| Header       | Description                                                         |
|--------------|---------------------------------------------------------------------|
| `X-Trace-ID` | Optional. If provided, echoed back and used as the request trace ID |

### Response Headers (all endpoints)

| Header             | Description                                                 |
|--------------------|-------------------------------------------------------------|
| `X-Trace-ID`       | Trace ID for this request (generated UUID v4 if not provided) |

### Response Headers (`POST /check` only)

| Header                | Description                                                   |
|-----------------------|---------------------------------------------------------------|
| `X-RateLimit-Limit`   | The configured quota for the matched route                    |
| `X-RateLimit-Remaining` | Tokens remaining in the current bucket                      |
| `X-RateLimit-Reset`   | Unix timestamp (seconds) when the bucket will be full         |
| `Retry-After`         | Seconds to wait before retrying (present only on `429` responses) |

---

## Route Matching

When `POST /check` is called, the service selects the most specific matching route from the client's policy:

1. **Exact match** (`route_type: exact`) — the `route` field must equal the policy route exactly. Exact matches always take priority over prefix matches.
2. **Prefix match** (`route_type: prefix`) — the `route` field must start with the policy route at a path boundary. `/api` matches `/api` and `/api/videos/123` but **not** `/apiv2`. When multiple prefix rules match, the longest prefix wins.
3. **Method matching** — `"*"` matches any HTTP method. Otherwise the match is case-insensitive (e.g. `"get"` matches `"GET"`).

If no rule matches, `POST /check` returns `404`.

---

## Identifier Types

The `identifier` field on a route policy controls how requests are bucketed — i.e. whether a limit is shared or per-user:

| Value           | Bucket key includes       | Use case                                                    |
|-----------------|---------------------------|-------------------------------------------------------------|
| `none`          | (nothing extra)           | One shared bucket for all callers of this client+route      |
| `session_id`    | `session_id` from request | Per-authenticated-session quota                             |
| `ip_user_agent` | `ip` + `user_agent`       | Per-device quota (IP + User-Agent couple)                   |

---

## Configuration

Settings are loaded by `internal/config/`. Resolution order, highest priority first:

1. Environment variable (e.g. `REDIS_HOST` overrides `redis.host`)
2. `config.yaml` in the working directory or `/app`
3. Built-in defaults

Missing config file is **not** an error — defaults + env vars are sufficient to boot.

```yaml
server:
  port: 8080       # listening port (int)
  timeout: 30s     # read/write timeout (Go duration)

redis:
  host: localhost
  port: 6379
  db: 0
  password: ""     # set via REDIS_PASSWORD env var
  tls: false       # set REDIS_TLS=true for ElastiCache in-transit encryption

policies:
  - client_id: mobile_app
    routes:
      - route: /api/videos      # path pattern
        route_type: prefix       # "exact" | "prefix"
        method: "*"              # HTTP method or "*" for all
        limit: 100               # requests allowed per window
        window: 1m               # time window (Go duration: 30s, 5m, 1h, …)
        identifier: session_id   # "none" | "session_id" | "ip_user_agent"

      - route: /api/upload
        route_type: exact
        method: POST
        limit: 10
        window: 1h
        identifier: ip_user_agent

  - client_id: web_app
    routes:
      - route: /
        route_type: prefix
        method: "*"
        limit: 500
        window: 1m
        identifier: none
```

### Environment variable overrides

| Variable          | Config key       | Example                  |
|-------------------|------------------|--------------------------|
| `SERVER_PORT`     | `server.port`    | `SERVER_PORT=9090`       |
| `SERVER_TIMEOUT`  | `server.timeout` | `SERVER_TIMEOUT=15s`     |
| `REDIS_HOST`      | `redis.host`     | `REDIS_HOST=redis`       |
| `REDIS_PORT`      | `redis.port`     | `REDIS_PORT=6380`        |
| `REDIS_DB`        | `redis.db`       | `REDIS_DB=1`             |
| `REDIS_PASSWORD`  | `redis.password` | `REDIS_PASSWORD=s3cret`  |
| `REDIS_TLS`       | `redis.tls`      | `REDIS_TLS=true`         |

> Env vars use `_` as the separator; Viper maps `redis.host` → `REDIS_HOST` via a key replacer registered in `config.Load()`.

### Observability environment variables

| Variable          | Effect                                                    |
|-------------------|-----------------------------------------------------------|
| `DD_AGENT_HOST`   | Enables DogStatsD metrics and DataDog APM tracing         |
| `DD_ENV`          | DataDog `env` tag (e.g. `production`, `staging`)          |
| `DD_VERSION`      | DataDog `version` tag for the running build               |

When `DD_AGENT_HOST` is unset, metrics and tracing are no-ops — the service starts cleanly with no external dependencies.

---

## Quick Start

**Prerequisites: Docker + Docker Compose. Nothing else.**

```bash
docker compose up --build
```

The service starts on port `8080`. Redis starts automatically.

### Try it

```bash
# Check a request
curl -s -X POST http://localhost:8080/check \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"mobile_app","route":"/api/videos/1","method":"GET","session_id":"abc"}' | jq

# List all clients
curl -s http://localhost:8080/policies | jq

# Inspect a client's routes
curl -s http://localhost:8080/policies/mobile_app | jq

# Health check
curl -s http://localhost:8080/health | jq
```

---

## Testing

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
# Integration tests (self-contained, no live server required)
docker compose --profile test run --rm test

# K6 load tests (starts app + Redis automatically)
docker compose --profile load-test run --rm k6-load
docker compose --profile load-test run --rm k6-spike
docker compose --profile load-test run --rm k6-endurance
```

---

## Test Suites

### Unit tests (`internal/...`)

Layer-isolated tests covering models, config loading, policy matching, the rate limiter wiring, the storage contract (MemoryStore against the same contract suite as RedisStore), and HTTP handlers.

Run with the race detector enabled:

```bash
go test -race ./internal/...
```

Storage contract tests against real Redis are gated by reachability — they skip cleanly if `localhost:6379` doesn't answer. To exercise the Lua-script path in CI without a Redis sidecar, you can swap `newRedisStoreOrSkip` for `miniredis.RunT(t)`.

### Integration tests (`tests/integration_test.go`)

Go end-to-end tests using `httptest` and a `MemoryStore` — no live server or Redis required.

Covers:
- Basic allow / deny / retry-after flow
- Quota isolation: by client, by route, by identifier (session / IP + user agent)
- Route matching: exact, prefix, wildcard method, method case insensitivity
- Input validation: missing required fields, unknown client, unknown route
- HTTP headers: `X-Trace-ID`, `X-RateLimit-*`, `Retry-After`
- Policy endpoints: list all clients, inspect routes per client
- Health endpoint: ok and degraded states
- Exact quota exhaustion (11th request denied when limit=10)
- Concurrent requests: 150 goroutines → exactly 100 allowed, 50 denied (atomicity test)
- Storage persistence across sequential requests
- Counter refill across window expiry
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

### Expected results

| Test suite  | Duration | Expected outcome                |
|-------------|----------|---------------------------------|
| Integration | ~5s      | 100% pass                       |
| Load        | ~2min    | p95 < 500ms, <10% failures      |
| Spike       | ~30s     | p99 < 2s, no panics             |
| Endurance   | ~11min   | Stable latency, no memory growth |

---

## CI/CD Pipeline

### Workflows

| Workflow | Trigger | Jobs |
|----------|---------|------|
| **PR Checks** | Every pull request to `main` | lint, unit tests (race + coverage), integration tests, load + spike tests, gosec scan |
| **Main Merge** | Push to `main` | All PR checks → Docker build → Trivy container scan → deployment smoke test |
| **Nightly Endurance** | `cron: 0 2 * * *` (or manual via `workflow_dispatch`) | K6 endurance test (~11 min) |

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

---

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

The app automatically falls back to in-memory storage and retries Redis reconnection every 10 seconds. The service continues enforcing rate limits during the outage, but each replica enforces against its own in-memory state (effective limit becomes N × configured limit across N replicas).

**`connection refused` to `[::1]:6379` from inside the container**

The app is dialling `localhost` from inside its container, ignoring `REDIS_HOST=redis`. This means env-var resolution isn't reaching Viper. Confirm:
- `REDIS_HOST` is actually exported in the container (`docker compose exec app env | grep REDIS`)
- `config.Load()` registers the key replacer (`SetEnvKeyReplacer(strings.NewReplacer(".", "_"))`); without it Viper looks for `REDIS.HOST` which can never exist in POSIX env

**Verifying Redis is the source of truth**

```bash
# Hit /check, then look at what Redis stores:
docker compose exec redis redis-cli KEYS 'rl:*'
docker compose exec redis redis-cli HGETALL rl:rl:partner:GET:/api/videos:global
# Should show "tokens" and "last_refill_ms" — proving the Lua script ran.

# Restart only the app and confirm the bucket survives:
docker compose restart app
# Send another /check, Remaining should pick up where it left off, not reset.
```
