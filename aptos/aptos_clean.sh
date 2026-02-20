#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="${1:-/tmp/aptos-dstest}"

# Kill any running aptos-node processes started by dsTest
pkill -f "aptos-node.*${BASE_DIR}/nodes" || true
pkill -f "aptos-node" || true

# Optional: wipe node data between iterations for determinism
rm -rf "${BASE_DIR}/nodes"/*/data || true