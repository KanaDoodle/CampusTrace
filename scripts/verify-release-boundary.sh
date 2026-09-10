#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
export GOWORK=off
[ "$(go list -m)" = "github.com/KanaDoodle/CampusTrace" ]
go mod download
module=$(go list -m -f '{{.Path}} {{.Version}}{{if .Replace}} REPLACED{{end}}' github.com/KanaDoodle/KanaRPC-Go)
[ "$module" = "github.com/KanaDoodle/KanaRPC-Go v0.1.0" ]
go test -count=1 ./internal/integration -run '^TestRepairPublicDependencyBoundary$'
go build ./...
printf 'PASS: workspace disabled; published KanaRPC-Go v0.1.0; public facade boundary.\n'
printf 'Remote clean-clone startup and dogfood must be verified separately.\n'
