SHELL := /bin/sh
.PHONY: deps build cli migrate seed run stop dev-run dev-stop test race vet integration eval loadgen
deps:
	docker compose up -d --wait
build:
	mkdir -p bin
	go build -o bin/ ./cmd/...
seed:
	go run ./cmd/seed
cli:
	mkdir -p bin
	GOWORK=off go build -o bin/campustrace ./cmd/campustrace
run: cli
	./bin/campustrace start
stop: cli
	./bin/campustrace stop
dev-run: build
	./scripts/start.sh
dev-stop:
	./scripts/stop.sh
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
integration:
	docker compose exec -T mysql mysql -uroot -plocal-root-only -e "CREATE DATABASE IF NOT EXISTS campustrace_test; GRANT ALL ON campustrace_test.* TO 'campus'@'%';"
	CAMPUS_INTEGRATION=1 go test -count=1 -v ./internal/integration

eval:
	go run ./cmd/eval
	go run ./cmd/eval -mode journey

.PHONY: matching-eval
matching-eval:
	go run ./cmd/match-eval -dataset internal/matcheval/testdata/examples.json -strict
loadgen:
	go run ./cmd/loadgen -n 100

# Local development only. The release go.mod intentionally has no replace.
.PHONY: dev-workspace verify-boundary
dev-workspace:
	@test ! -e go.work || { echo 'go.work already exists; inspect it before changing the dependency'; exit 1; }
	go work init .
	go work edit -go=1.25.9 -replace=github.com/KanaDoodle/KanaRPC-Go=../KanaRPC-Go
verify-boundary:
	./scripts/verify-release-boundary.sh

.PHONY: retrieval-eval
retrieval-eval:
	go run ./cmd/retrieval-eval

.PHONY: observability observability-stop observability-status observability-test
observability:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.observability.yml stop api worker analysis-1 analysis-2
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.observability.yml up -d --build --wait
observability-stop:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.observability.yml stop
observability-status:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.observability.yml ps
observability-test:
	sh scripts/observability-local.sh

.PHONY: search-stack search-stop search-status search-rebuild search-test
search-stack:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.search.yml up -d --build --wait
search-stop:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.search.yml stop
search-status:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.search.yml ps
search-rebuild:
	docker compose --file docker-compose.yml --file compose.app.yml --file compose.search.yml exec -T search /app/search rebuild
search-test:
	sh scripts/search-local.sh

.PHONY: performance performance-test performance-compare
performance:
	./scripts/performance-local.sh $(PERF_ARGS)
performance-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/performance -p 'test_*.py' -v
	node --test scripts/performance/contract.test.mjs
performance-compare:
	@test -n "$(PERF_BASELINE)" && test -n "$(PERF_CANDIDATE)" || { echo 'Set PERF_BASELINE and PERF_CANDIDATE to report.json paths'; exit 1; }
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/performance/report.py "$(PERF_CANDIDATE)" --baseline "$(PERF_BASELINE)"

KAFKA_COMPOSE = docker compose --file docker-compose.yml --file compose.app.yml --file compose.search.yml --file compose.kafka.yml
.PHONY: kafka-stack kafka-stop kafka-status kafka-replay kafka-test kafka-stack-test
kafka-stack:
	$(KAFKA_COMPOSE) run --rm kafka-init
	$(KAFKA_COMPOSE) up -d --build --wait api worker analysis-1 analysis-2 search notify
kafka-stop:
	$(KAFKA_COMPOSE) stop
kafka-status:
	$(KAFKA_COMPOSE) ps -a
kafka-replay:
	$(KAFKA_COMPOSE) exec -T worker /app/events replay $(EVENT_ARGS)
kafka-test:
	sh scripts/kafka-local.sh
kafka-stack-test:
	CAMPUS_SMOKE_KAFKA=1 sh scripts/observability-local.sh
