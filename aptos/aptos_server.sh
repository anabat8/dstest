#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./aptos_server.sh 0 /tmp/aptos-dstest
NODE_INDEX="${1:?need node index (0..)}"
BASE_DIR="${2:-/tmp/aptos-dstest}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"
APTOS_NODE_BIN="${APTOS_NODE_BIN:-${APTOS_ROOT}/target/release/aptos-node}"

NODE_DIR="${BASE_DIR}/nodes/v${NODE_INDEX}"

# ByzzFuzz: write Noise session secrets to a per-node file (enabled only if byzzfuzz feature compiled in)
# e.g. path for node 0 : /tmp/aptos-dstest/nodes/v0/noise_secrets.jsonl
export BYZZFUZZ_NOISE_SECRETS_PATH="${NODE_DIR}/noise_secrets.jsonl"

# optional: start fresh each run
rm -f "${BYZZFUZZ_NOISE_SECRETS_PATH}" || true

if [[ ! -x "${APTOS_NODE_BIN}" ]]; then
  echo "ERROR: aptos-node binary not found/executable: ${APTOS_NODE_BIN}"
  echo "Build it: (cd aptos-core && cargo build -p aptos-node --release)"
  exit 1
fi

test -f "${NODE_DIR}/node.yaml" || {
  echo "Missing ${NODE_DIR}/node.yaml (run aptos_make_node_configs.sh first)"
  exit 1
}

echo "Starting Aptos validator v${NODE_INDEX}"
echo "Config: ${NODE_DIR}/node.yaml"
echo "Data:   ${NODE_DIR}/data"

"${APTOS_NODE_BIN}" -f "${NODE_DIR}/node.yaml" &
PID=$!

cleanup() {
  echo "Stopping Aptos validator v${NODE_INDEX} (pid $PID)"
  kill -TERM "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

wait "$PID"