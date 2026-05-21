#!/usr/bin/env bash
set -euo pipefail

# Invoked each time the dsTest scheduler fires a client request.
#
# Submits a batch of transactions to one Aptos validator REST
# endpoints and exits.
#
# Each client worker uses a distinct pre-generated Aptos account identity. 
#
# Client identities are generated during localnet setup:
#   make client-accounts
# Client accounts are funded from the genesis root signer (0xA550C18) during each
# DSTest iteration.
#
# Identities live under:
#   ${BASE_DIR}/genesis/clients/cNN.yaml
#
# Usage:
#
#   ./aptos_client.sh [CLIENT_ID] [NODE_INDEX] [NUM_TXS]
#
# Example:
#
#   ./aptos_client.sh 3 1 20
#
# Meaning:
#   - use client account c03.yaml
#   - submit through validator REST node 1
#   - submit 20 txs
#
# Positional args:
#
#   CLIENT_ID
#       Index into the reusable client-account pool (selects cNN.yaml).
#       Across the ClientScripts list in aptos.yml, each CLIENT_ID must appear
#       at most once; otherwise concurrent invocations race on the same
#       account's sequence number. See ByzzFuzzScheduler.AvailableClients.
#
#   NODE_INDEX
#       Single validator index ("0")
#
#   NUM_TXS
#       Number of transactions to submit in this invocation.
#
# Environment vars:
#   BASE_PORT: REST API port for validator 0 (default: 8000)
#              Validator i REST port: BASE_PORT + i * 10
#
#   BASE_DIR: Root directory of the generated Aptos localnet (default: /tmp/aptos-dstest)
#
# Notes:
#
# - Transactions are submitted through validator REST APIs, but accounts are
#   global blockchain state replicated across all validators.
#
# - Funding an account through one validator does not bind the account to that
#   validator. The account can later submit transactions through any validator.
#
# - Using independent client accounts avoids shared sequence-number streams and
#   removes the need for global locking between concurrent dstest workers.

CLIENT_ID="${1:-0}"
NODE_INDEX="${2:-0}"
NUM_TXS="${3:-10}"

BASE_PORT="${BASE_PORT:-8000}"
BASE_DIR="${BASE_DIR:-/tmp/aptos-dstest}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${SCRIPT_DIR}/../cmd/aptos_client/main"

if [[ ! -x "${BIN}" ]]; then
  echo "ERROR: aptos_client binary not found: ${BIN}"
  echo "Build it with: make build-aptos-client"
  exit 1
fi

# -----------------------------------------------------------------------------
# Resolve client identity
# -----------------------------------------------------------------------------

CLIENTS_DIR="${BASE_DIR}/genesis/clients"

PADDED_CLIENT_ID=$(printf "%02d" "${CLIENT_ID}")

IDENTITY="${CLIENTS_DIR}/c${PADDED_CLIENT_ID}.yaml"

if [[ ! -f "${IDENTITY}" ]]; then
  echo "ERROR: missing client identity: ${IDENTITY}"
  exit 1
fi

# -----------------------------------------------------------------------------
# Resolve validator REST port
# -----------------------------------------------------------------------------

port=$(( BASE_PORT + NODE_INDEX * 10 ))

echo "[aptos_client] client=${CLIENT_ID} port=${port} txs=${NUM_TXS}"

# -----------------------------------------------------------------------------
# Invoke transaction submitter binary
# -----------------------------------------------------------------------------

"${BIN}" \
  --port "${port}" \
  --identity "${IDENTITY}" \
  --num-txs "${NUM_TXS}"
