#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
for name in api worker analysis-1 analysis-2 preparation; do
  if [ -f "bin/$name.pid" ]; then
    pid=$(cat "bin/$name.pid")
    command=$(ps -p "$pid" -o command= 2>/dev/null || true)
    case "$command" in *./bin/api*|*./bin/worker*|*./bin/analysis*|*uvicorn\ app:app\ --app-dir\ services/preparation*) kill -TERM "$pid" 2>/dev/null || true;; esac
    rm "bin/$name.pid"
  fi
done
