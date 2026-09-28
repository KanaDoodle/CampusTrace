#!/bin/sh
# Owned synthetic schema only. No model configuration or production data.
set -eu
cd "$(dirname "$0")/.."
export GOWORK=off
stamp=$(date +%Y%m%d%H%M%S)_$$
bench_db=campustrace_bench_$stamp
mysql_admin(){ docker compose exec -T mysql mysql -uroot -plocal-root-only "$@"; }
mysql_admin -e "CREATE DATABASE $bench_db; GRANT ALL ON $bench_db.* TO 'campus'@'%';" >/dev/null 2>&1
trap 'mysql_admin -e "DROP DATABASE $bench_db;" >/dev/null 2>&1' EXIT
export MYSQL_BENCH_DSN="campus:local-campus-only@tcp(127.0.0.1:13306)/$bench_db?parseTime=true&loc=UTC"
go run ./cmd/benchmark "$@"
