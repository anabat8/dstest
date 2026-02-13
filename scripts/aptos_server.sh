#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./aptos_server.sh 0 8000
NODE_INDEX="${1:?need node index (0..)}"
BASE_DIR="${2:-/tmp/aptos-dstest}"

#BASE_PORT="${2:-8000}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"
APTOS_NODE_BIN="${APTOS_NODE_BIN:-${APTOS_ROOT}/target/release/aptos-node}"

NODE_DIR="${BASE_DIR}/nodes/v${NODE_INDEX}"

if [[ ! -x "${APTOS_NODE_BIN}" ]]; then
  echo "ERROR: aptos-node binary not found/executable: ${APTOS_NODE_BIN}"
  echo "Build it: (cd aptos-core && cargo build -p aptos-node --release)"
  exit 1
fi

test -f "${NODE_DIR}/node.yaml" || {
  echo "Missing ${NODE_DIR}/node.yaml (run make_node_configs.sh first)"
  exit 1
}

echo "Starting Aptos validator v${NODE_INDEX}"
echo "Config: ${NODE_DIR}/node.yaml"
echo "Data:   ${NODE_DIR}/data"

exec "${APTOS_NODE_BIN}" -f "${NODE_DIR}/node.yaml"