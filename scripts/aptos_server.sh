#!/usr/bin/env bash
set -euo pipefail

# NUM_VALIDATORS=${1:-4}

# SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"

# cd "${APTOS_ROOT}"

# echo "[Aptos] Starting local swarm (${NUM_VALIDATORS} validators)"

# cargo run -p aptos-forge-cli -- \
#   --suite run_forever \
#   --num-validators "${NUM_VALIDATORS}" \
#   test local-swarm \
#   2>&1 | tee /tmp/forge.out

#######################################################################

# Usage:
#   ./aptos_server.sh 0 8000
NODE_INDEX="${1:?need node index (0..)}"
BASE_PORT="${2:-8000}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="${BASE_DIR:-/tmp/aptos-dstest}"
NODES_DIR="${BASE_DIR}/nodes"
NODE_DIR="${NODES_DIR}/v${NODE_INDEX}"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"
APTOS_NODE_BIN="${APTOS_NODE_BIN:-${APTOS_ROOT}/target/release/aptos-node}"

if [[ ! -x "${APTOS_NODE_BIN}" ]]; then
  echo "ERROR: aptos-node binary not found/executable: ${APTOS_NODE_BIN}"
  echo "Build it: (cd aptos-core && cargo build -p aptos-node --release)"
  exit 1
fi

test -f "${NODE_DIR}/node.yaml" || { echo "Missing ${NODE_DIR}/node.yaml (run aptos_make_node_configs.sh)"; exit 1; }

echo "Starting Aptos validator v${NODE_INDEX}"
echo "Config: ${NODE_DIR}/node.yaml"
echo "Data:   ${NODE_DIR}/data"

exec "${APTOS_NODE_BIN}" -f "${NODE_DIR}/node.yaml"