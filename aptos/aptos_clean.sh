#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="${1:-${BASE_DIR:-/tmp/aptos-dstest}}"
NODE_PATTERN="aptos-node.*${BASE_DIR}/nodes"

# Stop Aptos nodes and wait for them to release their ports
pkill -TERM -f "$NODE_PATTERN" || true

for _ in {1..100}; do
  if ! pgrep -f "$NODE_PATTERN" >/dev/null; then
    break
  fi
  sleep 0.1
done

# Force termination if graceful shutdown takes longer than 10 seconds
if pgrep -f "$NODE_PATTERN" >/dev/null; then
  echo "Aptos nodes did not stop after 10 seconds; forcing termination"
  pkill -KILL -f "$NODE_PATTERN" || true
  sleep 1
fi

# Wipe per-execution state while preserving generated node configurations
rm -rf "${BASE_DIR}/nodes"/*/data || true
rm -f "${BASE_DIR}/nodes"/*/noise_secrets.jsonl || true