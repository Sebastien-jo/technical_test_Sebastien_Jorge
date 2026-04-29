DC = docker compose

.PHONY: help up down logs build health \
        test-integration test-load test-spike test-endurance test-all full-test \
        clean

help:
	@echo ""
	@echo "Rate Limiter — available commands"
	@echo "Requirements: Docker + Docker Compose (nothing else)"
	@echo ""
	@echo "  Service management:"
	@echo "    make up               Start app + Redis"
	@echo "    make down             Stop all services"
	@echo "    make logs             Follow app logs"
	@echo "    make build            Rebuild app Docker image"
	@echo "    make health           Quick health check (requires curl)"
	@echo "    make clean            Remove containers, volumes, networks"
	@echo ""
	@echo "  Testing:"
	@echo "    make test-integration Run Go integration tests in Docker (~5s)"
	@echo "    make test-load        Run K6 normal load test (~2min)"
	@echo "    make test-spike       Run K6 spike test (~30s)"
	@echo "    make test-endurance   Run K6 endurance test (~11min)"
	@echo "    make test-all         Run integration + all K6 tests"
	@echo "    make full-test        up → test-all → down"
	@echo ""

up:
	$(DC) up -d redis app

down:
	$(DC) down

logs:
	$(DC) logs -f app

build:
	$(DC) build app

health:
	curl -sf http://localhost:8080/health

test-integration:
	$(DC) --profile test run --rm test

test-load:
	$(DC) --profile load-test up -d redis app
	$(DC) --profile load-test run --rm k6-load

test-spike:
	$(DC) --profile load-test up -d redis app
	$(DC) --profile load-test run --rm k6-spike

test-endurance:
	$(DC) --profile load-test up -d redis app
	$(DC) --profile load-test run --rm k6-endurance

test-all: test-integration test-load test-spike test-endurance
	@echo ""
	@echo "All tests completed."

full-test:
	$(MAKE) up
	$(MAKE) test-integration
	$(MAKE) test-load
	$(MAKE) test-spike
	$(MAKE) test-endurance
	$(MAKE) down

clean:
	$(DC) down -v --remove-orphans
