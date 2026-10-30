#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
export ADVERSARIAL_TESTS="${MODEL_CONTROL_TESTS:-TestModelControl}"
export CAMPUS_ADVERSARIAL_EVIDENCE="$PWD/bin/verification/model-control-20261030/processes.jsonl"
mkdir -p "$(dirname "$CAMPUS_ADVERSARIAL_EVIDENCE")"
exec sh scripts/adversarial-local.sh
