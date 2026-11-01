#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
export GOWORK=off
[ "$(go list -m)" = "github.com/KanaDoodle/CampusTrace" ]
go mod download
if go list -m all | grep -q 'github.com/KanaDoodle/KanaRPC-Go'; then
 printf 'FAIL: retired KanaRPC-Go module remains in dependency graph.\n' >&2; exit 1
fi
module=$(go list -m -f '{{.Path}} {{.Version}}{{if .Replace}} REPLACED{{end}}' github.com/KanaDoodle/KanaRPC)
[ "$module" = "github.com/KanaDoodle/KanaRPC v0.0.0-20261031172914-7b63b80f0544" ]
go test -count=1 ./internal/integration -run '^TestRepairPublicDependencyBoundary$'
go build ./...
# Search's independent image must not acquire Core persistence or RPC clients.
search_deps=$(go list -deps ./cmd/search)
for dependency in $search_deps;do
 case "$dependency" in
  github.com/KanaDoodle/CampusTrace/internal/observability|github.com/KanaDoodle/CampusTrace/internal/eventbus|github.com/KanaDoodle/CampusTrace/internal/jobevent|github.com/KanaDoodle/CampusTrace/internal/searchapi|github.com/KanaDoodle/CampusTrace/internal/searchservice) ;;
  github.com/KanaDoodle/CampusTrace/internal/*|github.com/go-sql-driver/*|github.com/redis/*|go.etcd.io/*|github.com/KanaDoodle/KanaRPC*)
   printf 'FAIL: Search imported a Core dependency: %s\n' "$dependency" >&2;exit 1;;
 esac
done
printf 'PASS: independent Search dependency boundary.\n'
printf 'PASS: workspace disabled; published KanaRPC v0.0.0-20261031172914-7b63b80f0544; public API boundary.\n'
printf 'Remote clean-clone startup and dogfood must be verified separately.\n'
