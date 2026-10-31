#!/bin/sh
# Disposable local demo / regression stack. Never reads .env or existing volumes.
set -eu
mode=${1:-demo}
case "$mode" in demo|smoke|test|scheduler-test) ;; *) echo 'Usage: scripts/journey-local.sh [demo|smoke|test|scheduler-test]' >&2; exit 2;; esac
cd "$(dirname "$0")/.."
task_dir=$(mktemp -d /tmp/campustrace-journey.XXXXXX)
mysql_name="campustrace-journey-mysql-$$"
redis_name="campustrace-journey-redis-$$"
app_pid=''
cleanup() {
  if [ -n "$app_pid" ]; then kill "$app_pid" 2>/dev/null || true; wait "$app_pid" 2>/dev/null || true; fi
  docker rm -f "$mysql_name" "$redis_name" >/dev/null 2>&1 || true
  rm -rf "$task_dir"
}
trap cleanup EXIT HUP INT TERM
export GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
if [ "$mode" = demo ] || [ "$mode" = smoke ]; then go build -o "$task_dir/journey-demo" ./cmd/journey-demo; fi
docker run --rm -d --name "$mysql_name" --tmpfs /var/lib/mysql --publish 127.0.0.1::3306 \
  -e MYSQL_ROOT_PASSWORD=synthetic-test-only -e MYSQL_DATABASE=campustrace_journey_demo \
  -e MYSQL_USER=journey -e MYSQL_PASSWORD=synthetic-test-only mysql:8.4 --mysqlx=OFF >/dev/null
docker run --rm -d --name "$redis_name" --publish 127.0.0.1::6379 redis:7.4-alpine --save '' --appendonly no >/dev/null
attempt=0
until docker exec "$mysql_name" mysql -h127.0.0.1 -ujourney -psynthetic-test-only -e 'SELECT 1' campustrace_journey_demo >/dev/null 2>&1; do
  attempt=$((attempt+1)); if [ "$attempt" -ge 60 ]; then echo 'Disposable MySQL did not become ready.' >&2; exit 1; fi
  sleep 1
done
mysql_port=$(docker port "$mysql_name" 3306/tcp | sed -n 's/^127.0.0.1://p')
redis_port=$(docker port "$redis_name" 6379/tcp | sed -n 's/^127.0.0.1://p')
dsn="journey:synthetic-test-only@tcp(127.0.0.1:$mysql_port)/campustrace_journey_demo?parseTime=true&loc=UTC"
export MYSQL_TEST_ADMIN_DSN="root:synthetic-test-only@tcp(127.0.0.1:$mysql_port)/mysql?parseTime=true&loc=UTC"
if [ "$mode" = scheduler-test ]; then
  export CAMPUS_INTEGRATION=1 CAMPUS_LIVE_SOURCES=0 MYSQL_DSN="$dsn" MYSQL_TEST_DSN="$dsn" REDIS_ADDR="127.0.0.1:$redis_port"
  docker exec "$mysql_name" mysql -uroot -psynthetic-test-only -e "CREATE DATABASE campustrace_migration_test; GRANT ALL ON campustrace_migration_test.* TO 'journey'@'%';"
  export MYSQL_MIGRATION_TEST_DSN="journey:synthetic-test-only@tcp(127.0.0.1:$mysql_port)/campustrace_migration_test?parseTime=true&loc=UTC"
  go test -race -count=1 -v ./internal/pipeline ./internal/scheduling
  go test -race -count=1 -v ./internal/integration -run 'Test(Scheduler|Application|AutoCompany|Company|Whole|Campaign|MatchRun|Durable|ExpiredTask|Release(Parser|Generation|Receipt|ActiveGeneration|BoundTask|AnalysisVersion|PrepareHistory|StaleEvaluate|PEL)|SourceImport|Repair(Cold|LegacyCache|Poison|Manual|Metadata|LegacyMigration))'
elif [ "$mode" = test ]; then
  CAMPUS_INTEGRATION=1 CAMPUS_LIVE_SOURCES=0 MYSQL_DSN="$dsn" MYSQL_TEST_DSN="$dsn" REDIS_ADDR="127.0.0.1:$redis_port" \
    go test -race -count=1 -v ./internal/integration -run 'Test(Application|AutoCompany|Company|Whole|Campaign|MatchRun)'
else
  "$task_dir/journey-demo" -dsn "$dsn" -redis "127.0.0.1:$redis_port" -http "127.0.0.1:${JOURNEY_HTTP_PORT:-23380}" &
  app_pid=$!
  if [ "$mode" = smoke ]; then
    python3 - "http://127.0.0.1:${JOURNEY_HTTP_PORT:-23380}" <<'PY'
import sys,time,urllib.request
for _ in range(100):
    try:
        urllib.request.urlopen(sys.argv[1]+'/healthz',timeout=1).close()
        break
    except OSError: time.sleep(.1)
else: raise SystemExit('Synthetic HTTP demo did not become ready')
PY
    python3 scripts/smoke-journey.py --url "http://127.0.0.1:${JOURNEY_HTTP_PORT:-23380}"
  else
    wait "$app_pid"
  fi
fi
