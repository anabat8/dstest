#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"
APTOS_CLI="${APTOS_ROOT}/target/cli/aptos"
BASE_DIR="/tmp/aptos-dstest"
GENESIS_DIR="${BASE_DIR}/genesis"       # here we store all validator generated config files

BASE_PORT=6001
NUM_NODES=4

CHAIN_ID=42
ROOT_KEY_HEX="0x$(openssl rand -hex 32)"
EPOCH_DURATION_SECS=7200
MIN_STAKE=1

mkdir -p "${GENESIS_DIR}"

USERS=()

for i in $(seq 0 $((NUM_NODES - 1))); do
    ID="n${i}"
    PORT=$((BASE_PORT + i))
    OUT_NODE_DIR="${BASE_DIR}/node_${ID}"
    KEY_DIR="${BASE_DIR}/${ID}_keys"

    USERS+=("${ID}")

    ${APTOS_CLI} genesis generate-keys --output-dir "${KEY_DIR}"

    ${APTOS_CLI} genesis set-validator-configuration \
        --owner-public-identity-file "${KEY_DIR}/public-keys.yaml" \
        --username ${ID} \
        --validator-host localhost:${PORT} \
        --full-node-host localhost:${PORT} \
        --local-repository-dir "${OUT_NODE_DIR}"
    
    # Copy validator YAML into genesis dir
    cp -r "${OUT_NODE_DIR}/${ID}" "${GENESIS_DIR}/"
done

# LAYOUT="root_key: \"${ROOT_KEY_HEX}\"
# users:
# $(printf '%s\n' "${USERS[@]}" | sed 's/^/- /')
# epoch_duration_secs: ${EPOCH_DURATION_SECS}
# chain_id: ${CHAIN_ID}"

# echo "${LAYOUT}" > "${GENESIS_DIR}/layout.yaml"

cat > "${GENESIS_DIR}/layout.yaml" <<EOF
root_key: "${ROOT_KEY_HEX}"
chain_id: ${CHAIN_ID}
epoch_duration_secs: ${EPOCH_DURATION_SECS}
users:
$(printf '  - %s\n' "${USERS[@]}")
EOF

#mkdir -p "${GENESIS_DIR}/framework"
#cp -r "${APTOS_ROOT}/aptos_framework_release}" "${GENESIS_DIR}/framework"

${APTOS_CLI} genesis generate-genesis \
  --local-repository-dir "${GENESIS_DIR}" \
  --output-dir "${GENESIS_DIR}"