#!/bin/sh
# Starts and cleans only its own synthetic stack. Does not load .env.
set -eu
export PYTHONDONTWRITEBYTECODE=1
cd "$(dirname "$0")/.."
exec python3 scripts/performance/lab.py "$@"
