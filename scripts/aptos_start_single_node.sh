#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"
APTOS_CLI="${APTOS_ROOT}/target/cli/aptos"
ID=$1

${APTOS_CLI} node run-local-testnet
--testnet-dir /tmp/aptos-dstest/node_${ID}
--config-path /tmp/aptos-dstest/node_${ID}/node.yaml