#!/usr/bin/env bash
set -euo pipefail

NUM_VALIDATORS=${1:-4}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"

cd "${APTOS_ROOT}"

echo "[Aptos] Starting local swarm (${NUM_VALIDATORS} validators)"

cargo run -p aptos-forge-cli -- \
  --suite run_forever \
  --num-validators "${NUM_VALIDATORS}" \
  test local-swarm \
  2>&1 | tee /tmp/forge.out