#!/usr/bin/env bash
set -euo pipefail

# Generate reusable Aptos workload accounts.
#
# Usage:
#   Run once after aptos_prepare_localnet.sh.
#
# Result:
#   ${BASE_DIR}/genesis/clients/cNN.yaml
#
# Each YAML contains:
#   - account_address
#   - account_private_key
#
# Accounts will be funded later (not in this script) from:
#   0xA550C18 (core_resources / genesis root signer)

NUM_CLIENT_ACCOUNTS="${NUM_CLIENT_ACCOUNTS:-8}"

BASE_DIR="${BASE_DIR:-/tmp/aptos-dstest}"
GENESIS_DIR="${GENESIS_DIR:-${BASE_DIR}/genesis}"

APTOS_CLI="${APTOS_CLI:-${APTOS_CORE}/target/cli/aptos}"

CLIENTS_DIR="${GENESIS_DIR}/clients"

echo "[client-accounts] generating ${NUM_CLIENT_ACCOUNTS} workload accounts"

rm -rf "${CLIENTS_DIR}"
mkdir -p "${CLIENTS_DIR}"

# -----------------------------------------------------------------------------
# Generate accounts
# -----------------------------------------------------------------------------

for i in $(seq 0 $((NUM_CLIENT_ACCOUNTS - 1))); do
    PADDED=$(printf "%02d" "${i}")

    TMP_DIR="${CLIENTS_DIR}/.tmp_c${PADDED}"
    mkdir -p "${TMP_DIR}"

    "${APTOS_CLI}" genesis generate-keys \
        --output-dir "${TMP_DIR}" >/dev/null

    mv "${TMP_DIR}/private-keys.yaml" \
       "${CLIENTS_DIR}/c${PADDED}.yaml"

    rm -rf "${TMP_DIR}"

    echo "  generated c${PADDED}.yaml"
done
