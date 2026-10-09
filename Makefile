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
