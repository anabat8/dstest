#!/usr/bin/env python3
"""
Patch Aptos node.yaml validator_network seeds to route through dsTest interceptors
and ensure peers are treated as Validator (not ValidatorFullNode).

Using 'seeds' instead of 'seed_addrs' is critical because seed_addrs always assigns PeerRole::ValidatorFullNode, 
but we need PeerRole::Validator for validators to recognize each other and establish consensus connections.

This is needed when dsTest sits between nodes and rewrites the actual
TCP ports used for validator-to-validator connections.

Address format: /ip4/127.0.0.1/tcp/<port>/noise-ik/<pubkey_hex>/handshake/0

Expected environment variables:
  - BASE_DIR   : /tmp/aptos-dstest
  - CONFIG     : path to dsTest config YAML, e.g. dstest/aptos/configs/aptos.yml
                (if not set, defaults to <repo>/aptos/configs/aptos.yml)

Node layout expected (created by aptos_make_node_configs.sh):
  - ${BASE_DIR}/nodes/v0/node.yaml
  - ${BASE_DIR}/nodes/v0/genesis/validator-identity.yaml
  - ${BASE_DIR}/nodes/v1/...
"""

import os
import sys
from pathlib import Path
import yaml
from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey

# -----------------------------
# Resolve env + paths
# -----------------------------
BASE_DIR = Path(os.environ.get("BASE_DIR", "/tmp/aptos-dstest"))
# Prefer CONFIG if Makefile passes it; otherwise assume dstest/aptos/configs/aptos.yml
CONFIG_ENV = os.environ.get("CONFIG")

if CONFIG_ENV:
    APTOS_YML = Path(CONFIG_ENV)
else:
    # script is in dstest/aptos/, so ../aptos/configs/aptos.yml relative to here is configs/aptos.yml
    ROOT = Path(__file__).resolve().parent
    APTOS_YML = ROOT / "configs" / "aptos.yml"

NODES_DIR = BASE_DIR / "nodes"


def fatal(msg: str, code: int = 1):
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(code)


# -----------------------------
# Load dsTest config
# -----------------------------
if not APTOS_YML.exists():
    fatal(f"dsTest config not found: {APTOS_YML}")

with open(str(APTOS_YML), "r") as f:
    doc = yaml.safe_load(f) or {}

base_interceptor = int(doc.get("NetworkConfig", {}).get("BaseInterceptorPort", 10000))
num_replicas = int(doc.get("ProcessConfig", {}).get("NumReplicas", 4))


# -----------------------------
# Gather node dirs
# -----------------------------
if not NODES_DIR.exists():
    fatal(f"nodes dir not found: {NODES_DIR} (did you run make node-configs?)")

node_dirs = sorted([p for p in NODES_DIR.iterdir() if p.is_dir() and p.name.startswith("v")])

if not node_dirs:
    fatal(f"no node dirs found under {NODES_DIR} (expected v0, v1, ...)")

# -----------------------------
# Key derivation helpers
# -----------------------------
def derive_public_key_hex(private_key_hex: str) -> str:
    if private_key_hex.startswith("0x") or private_key_hex.startswith("0X"):
        private_key_hex = private_key_hex[2:]
    private_bytes = bytes.fromhex(private_key_hex)
    private_key = X25519PrivateKey.from_private_bytes(private_bytes)
    public_key = private_key.public_key()
    return public_key.public_bytes_raw().hex()


# -----------------------------
# Read validator identities
# -----------------------------
node_info = {}
for nd in node_dirs:
    # v0 -> 0
    try:
        idx = int(nd.name[1:])
    except ValueError:
        continue

    id_file = nd / "genesis" / "validator-identity.yaml"
    if not id_file.exists():
        fatal(f"{nd} missing genesis/validator-identity.yaml")

    with open(id_file, "r") as f:
        data = yaml.safe_load(f) or {}

    account_address = data.get("account_address", "")
    network_private_key = data.get("network_private_key", "")

    if not account_address or not network_private_key:
        fatal(f"missing fields in {id_file}")

    if account_address.startswith("0x"):
        account_address = account_address[2:]

    network_public_key = derive_public_key_hex(network_private_key)
    node_info[idx] = {"account_address": account_address, "network_public_key": network_public_key}
    print(f"Node {idx}: account={account_address[:16]}..., pubkey={network_public_key[:16]}...")


# -----------------------------
# Interceptor port mapping
# -----------------------------
def get_interceptor_port(sender: int, receiver: int) -> int:
    # Directional mapping: sender*num_replicas + receiver
    id_ = sender * num_replicas + receiver
    return base_interceptor + id_


# -----------------------------
# Patch node.yaml seeds
# -----------------------------
for nd in node_dirs:
    try:
        idx = int(nd.name[1:])
    except ValueError:
        continue

    node_yaml = nd / "node.yaml"
    if not node_yaml.exists():
        fatal(f"Missing {node_yaml}")

    with open(node_yaml, "r") as f:
        cfg = yaml.safe_load(f) or {}

    # Build seeds with role: Validator (NOT seed_addrs which defaults to ValidatorFullNode)
    seeds = {}
    for j, info in node_info.items():
        if j == idx:
            continue
        port = get_interceptor_port(idx, j)
        pubkey = info["network_public_key"]
        addr = f"/ip4/127.0.0.1/tcp/{port}/noise-ik/0x{pubkey}/handshake/0"
        peer_id = info["account_address"]
        seeds[peer_id] = {
            "addresses": [addr],
            "keys": [f"0x{pubkey}"],
            "role": "Validator",
        }

    # Remove old seed_addrs if present, set seeds instead
    vnet = cfg.setdefault("validator_network", {})
    if not isinstance(vnet, dict):
        fatal(f"validator_network is not a mapping in {node_yaml}")

    vnet.pop("seed_addrs", None)
    vnet["seeds"] = seeds

    with open(node_yaml, "w") as f:
        yaml.dump(cfg, f, default_flow_style=False, sort_keys=False)

    print(f"Updated {nd.name} with seeds (role=Validator): {list(seeds.keys())}")

print("Done!")