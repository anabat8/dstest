#!/usr/bin/env bash
set -euo pipefail

# ------------------------------------------
# Resolve paths and environment variables
# ------------------------------------------

# Expect Makefile to pass these (or rely on defaults):
#   APTOS_CORE           : path to aptos-core
#   APTOS_CLI            : path to aptos CLI binary
#   BASE_DIR             : where genesis and node configurations live
#   GENESIS_DIR          : path to the genesis directory
#   FRAMEWORK_MRB        : path to the framework.mrb
#   NUM_NODES            : number of replicas in the local network
#   CHAIN_ID             : blockchain's id  (used for generating the layout.yaml, essential for genesis)
#   EPOCH_DURATIONS_SECS : timing of epochs (used for generating the layout.yaml, essential for genesis)
#   PYTHON_BIN           : python with pyyaml installed (e.g., venv)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

APTOS_CORE="${APTOS_CORE:-${SCRIPT_DIR}/../../aptos-core}"
APTOS_CLI="${APTOS_CLI:-${APTOS_CORE}/target/cli/aptos}"

BASE_DIR="${BASE_DIR:-/tmp/aptos-dstest}"
GENESIS_DIR="${GENESIS_DIR:-${BASE_DIR}/genesis}"
FRAMEWORK_MRB="${FRAMEWORK_MRB:-${GENESIS_DIR}/framework.mrb}"

NUM_NODES="${NUM_NODES:-4}"
CHAIN_ID="${CHAIN_ID:-42}"
EPOCH_DURATION_SECS="${EPOCH_DURATION_SECS:-7200}"

PYTHON_BIN="${PYTHON_BIN:-python3}"

# Validator host + fullnode host in genesis are used for discovery/addresses; 
# they do not have to equal REST API ports.
VAL_NET_BASE="${VAL_NET_BASE:-6100}"     # validator network port base for advertised addr
FN_NET_BASE="${FN_NET_BASE:-6200}"       # fullnode/VFN advertised addr base

# ------------------------------------------
# [0/3]: Sanity checks
# ------------------------------------------
test -d "${GENESIS_DIR}" || {
  echo "ERROR: Missing GENESIS_DIR=${GENESIS_DIR}"
  echo "Run: make genesis"
  exit 1
}

test -x "${APTOS_CLI}" || {
  echo "ERROR: aptos CLI not executable: ${APTOS_CLI}"
  echo "Run: make build-aptos"
  exit 1
}

test -f "${FRAMEWORK_MRB}" || {
  echo "ERROR: Missing ${FRAMEWORK_MRB}"
  echo "Run: make framework (then rerun make genesis)"
  exit 1
}

"${PYTHON_BIN}" -c 'import yaml' >/dev/null 2>&1 || {
  echo "ERROR: PYTHON_BIN cannot import PyYAML (import yaml failed)."
  echo "Run: make setup and ensure PYTHON_BIN points to the venv python."
  exit 1
}

# ------------------------------------------
# [1/3]: Root keys + layout.yaml
# ------------------------------------------

echo "[1/3] Generating root keys + layout.yaml..."
ROOT_KEYS_DIR="${GENESIS_DIR}/root"
rm -rf "${ROOT_KEYS_DIR}"
mkdir -p "${ROOT_KEYS_DIR}"

"${APTOS_CLI}" genesis generate-keys --output-dir "${ROOT_KEYS_DIR}" >/dev/null
test -f "${ROOT_KEYS_DIR}/public-keys.yaml" || { echo "Missing ${ROOT_KEYS_DIR}/public-keys.yaml"; exit 1; }

# Extract root account public key from YAML
ROOT_KEY_HEX="$(
"${PYTHON_BIN}" - "${ROOT_KEYS_DIR}/public-keys.yaml" <<'PY'
import sys, yaml, pathlib

p = pathlib.Path(sys.argv[1])
d = yaml.safe_load(p.read_text())

def find_key(obj):
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k in ("account_public_key", "account_public_key_hex", "public_key"):
                if isinstance(v, str) and v.strip():
                    return v.strip()
            res = find_key(v)
            if res:
                return res
    elif isinstance(obj, list):
        for it in obj:
            res = find_key(it)
            if res:
                return res
    return None

key = find_key(d)
if not key:
    raise SystemExit(f"Could not find account public key in {p}")

# Normalize 0x prefix
if not key.startswith("0x"):
    key = "0x" + key
print(key)
PY
)"

echo "Root public key: ${ROOT_KEY_HEX}"

if [[ "${ROOT_KEY_HEX}" != 0x* ]]; then
  ROOT_KEY_HEX="0x${ROOT_KEY_HEX}"
fi

USERS=()
for i in $(seq 0 $((NUM_NODES - 1))); do USERS+=("n${i}"); done

# Layout defaults
ALLOW_NEW_VALIDATORS="${ALLOW_NEW_VALIDATORS:-true}"
IS_TEST="${IS_TEST:-true}"

MIN_STAKE="${MIN_STAKE:-1}"
MAX_STAKE="${MAX_STAKE:-1000000000}"
MIN_VOTING_THRESHOLD="${MIN_VOTING_THRESHOLD:-0}"

RECURRING_LOCKUP_DURATION_SECS="${RECURRING_LOCKUP_DURATION_SECS:-86400}"
REQUIRED_PROPOSER_STAKE="${REQUIRED_PROPOSER_STAKE:-0}"
REWARDS_APY_PERCENTAGE="${REWARDS_APY_PERCENTAGE:-10}"

VOTING_DURATION_SECS="${VOTING_DURATION_SECS:-60}"
VOTING_POWER_INCREASE_LIMIT="${VOTING_POWER_INCREASE_LIMIT:-20}"

cat > "${GENESIS_DIR}/layout.yaml" <<EOF
root_key: "${ROOT_KEY_HEX}"
chain_id: ${CHAIN_ID}
users:
$(printf '  - %s\n' "${USERS[@]}")

# Localnet / test toggles
allow_new_validators: ${ALLOW_NEW_VALIDATORS}
is_test: ${IS_TEST}

# Epoch / governance timing
epoch_duration_secs: ${EPOCH_DURATION_SECS}
voting_duration_secs: ${VOTING_DURATION_SECS}
voting_power_increase_limit: ${VOTING_POWER_INCREASE_LIMIT}

# Staking / validator-set thresholds
min_stake: ${MIN_STAKE}
max_stake: ${MAX_STAKE}
min_voting_threshold: ${MIN_VOTING_THRESHOLD}
recurring_lockup_duration_secs: ${RECURRING_LOCKUP_DURATION_SECS}
required_proposer_stake: ${REQUIRED_PROPOSER_STAKE}

# Rewards
rewards_apy_percentage: ${REWARDS_APY_PERCENTAGE}

# On-chain config blocks
on_chain_consensus_config:
  V1:
    decoupled_execution: false
    back_pressure_limit: 0
    exclude_round: 0
    proposer_election_type:
      rotating_proposer: 1
    max_failed_authors_to_store: 100

on_chain_execution_config:
  V1:
    transaction_shuffler_type: no_shuffling

# JWK / keyless stuff: empty list for localnet
initial_jwks: []
EOF

# -------------------------------------------------------------
# [2/3] Per-validator keys + validator config (in GENESIS_DIR)
# -------------------------------------------------------------

echo "[2/3] Generating validator keys + validator configuration files..."

for i in $(seq 0 $((NUM_NODES - 1))); do
  ID="n${i}"
  KEY_DIR="${GENESIS_DIR}/${ID}_keys"
  rm -rf "${KEY_DIR}"
  mkdir -p "${KEY_DIR}"

  "${APTOS_CLI}" genesis generate-keys --output-dir "${KEY_DIR}" >/dev/null

  VAL_HOST="127.0.0.1:$((VAL_NET_BASE + i))"
  FN_HOST="127.0.0.1:$((FN_NET_BASE + i))"

  "${APTOS_CLI}" genesis set-validator-configuration \
    --owner-public-identity-file "${KEY_DIR}/public-keys.yaml" \
    --username "${ID}" \
    --validator-host "${VAL_HOST}" \
    --full-node-host "${FN_HOST}" \
    --join-during-genesis \
    --local-repository-dir "${GENESIS_DIR}" >/dev/null
  
  # Per-node configuration name.
  if [[ -f "${GENESIS_DIR}/validator-configuration.yaml" ]]; then
    mv -f "${GENESIS_DIR}/validator-configuration.yaml" \
      "${GENESIS_DIR}/${ID}.validator-configuration.yaml"
  fi
done

# ---------------------------------------
# [3/3] Generate genesis.blob + waypoint
# ---------------------------------------
echo "[3/3] Generating genesis.blob + waypoint..."
test -f "${GENESIS_DIR}/layout.yaml" || { echo "Missing ${GENESIS_DIR}/layout.yaml"; exit 1; }
test -f "${FRAMEWORK_MRB}" || { echo "Missing ${FRAMEWORK_MRB}"; exit 1; }

RUST_BACKTRACE=1 \
"${APTOS_CLI}" genesis generate-genesis \
  --assume-yes \
  --local-repository-dir "${GENESIS_DIR}" \
  --output-dir "${GENESIS_DIR}"

echo "  Done. Check:"
echo "  ${GENESIS_DIR}/genesis.blob"
echo "  ${GENESIS_DIR}/waypoint.txt"
echo "  ${GENESIS_DIR}/layout.yaml"
echo "  ${GENESIS_DIR}/framework/"
echo "  ${GENESIS_DIR}/*.validator-configuration.yaml"