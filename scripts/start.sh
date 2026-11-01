#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Optional user-authorized model settings; ignored by Git and image builds.
if [ -f .env.model.local ]; then
  set -a
  . ./.env.model.local
  set +a
fi
mkdir -p bin
if [ -f bin/api.pid ] && kill -0 "$(cat bin/api.pid)" 2>/dev/null; then echo 'CampusTrace is already running'; exit 1; fi
for name in api worker analysis-1 analysis-2 preparation; do
  if [ -s "bin/$name.log" ]; then mv "bin/$name.log" "bin/$name.log.previous"; fi
done
export PREPARATION_SERVICE_TOKEN="${PREPARATION_SERVICE_TOKEN:-local-preparation-service-change-this-2027}"
export PREPARATION_GRAPH_URL="${PREPARATION_GRAPH_URL:-http://127.0.0.1:18110}"
export PREPARATION_GO_URL="${PREPARATION_GO_URL:-http://${HTTP_ADDR:-127.0.0.1:8080}}"
if [ ! -x services/preparation/.venv/bin/python ]; then echo 'Run make preparation-install first' >&2; exit 1; fi
env -u LLM_API_KEY -u LLM_URL -u LLM_MODEL services/preparation/.venv/bin/python -m uvicorn app:app --app-dir services/preparation --host 127.0.0.1 --port 18110 --workers 1 --no-access-log >bin/preparation.log 2>&1 & echo $! >bin/preparation.pid
export JWT_SECRET="${JWT_SECRET:-local-campus-demo-secret-change-me-2027}"
INSTANCE_ID=analysis-1 ANALYSIS_HEALTH_ADDR=127.0.0.1:19191 ANALYSIS_ADDR=127.0.0.1:19091 ./bin/analysis >bin/analysis-1.log 2>&1 & echo $! >bin/analysis-1.pid
INSTANCE_ID=analysis-2 ANALYSIS_HEALTH_ADDR=127.0.0.1:19192 ANALYSIS_ADDR=127.0.0.1:19092 ./bin/analysis >bin/analysis-2.log 2>&1 & echo $! >bin/analysis-2.pid
./bin/worker >bin/worker.log 2>&1 & echo $! >bin/worker.pid
./bin/api >bin/api.log 2>&1 & echo $! >bin/api.pid
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if curl --fail --silent "$PREPARATION_GRAPH_URL/healthz" >/dev/null && curl --fail --silent "http://${HTTP_ADDR:-127.0.0.1:8080}/readyz" >/dev/null && curl --fail --silent http://127.0.0.1:19191/readyz >/dev/null && curl --fail --silent http://127.0.0.1:19192/readyz >/dev/null; then ready=1; break; fi
  sleep 1
done
if [ "$ready" != "1" ]; then ./scripts/stop.sh; echo 'Startup failed; inspect bin/*.log' >&2; exit 1; fi
for name in api worker analysis-1 analysis-2 preparation; do
  if ! kill -0 "$(cat "bin/$name.pid")" 2>/dev/null; then ./scripts/stop.sh; echo "Process $name failed; inspect bin/*.log" >&2; exit 1; fi
done
printf 'CampusTrace: http://127.0.0.1:8080\nLogs: bin/*.log\nStop: make dev-stop\n'

if [ "${FOREGROUND:-0}" = "1" ]; then
  trap './scripts/stop.sh' EXIT INT TERM
  wait
fi
