# Rate Limiter Service

A production-ready HTTP rate limiting service built in Go with Redis storage and an in-memory fallback.

## Architecture

```
cmd/       — entrypoint
internal/
  models/         — domain types
  service/        — business logic
  storage/        — Redis + in-memory backends
  handler/        — HTTP handlers (Gin)
tests/            — integration tests
```

## Quick Start

### With Docker Compose

```bash
docker compose up --build
```

### Locally

```bash
go mod tidy
go run ./cmd
```

The server starts on port `8080` by default.

## Endpoints

| Method | Path      | Description        |
|--------|-----------|--------------------|
| GET    | /health   | Health check       |

## Configuration

Settings are loaded from `config.yaml` and can be overridden with environment variables (e.g. `REDIS_HOST`, `SERVER_PORT`).

```yaml
server:
  port: "8080"
  timeout: 30s

redis:
  host: localhost
  port: 6379
  db: 0
```

## Development

```bash
# Run tests
go test ./...

# Build binary
go build -o rate-limiter ./cmd/server
```
