#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="${1:-${BASE_DIR:-/tmp/aptos-dstest}}"
NODE_PATTERN="aptos-node.*${BASE_DIR}/nodes"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DSTEST_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
PYTHON_BIN="${PYTHON_BIN:-${DSTEST_ROOT}/.venv/bin/python3}"

if [[ ! -x "$PYTHON_BIN" ]]; then
  PYTHON_BIN=python3
fi

# Stop Aptos nodes and wait for the process set to remain empty. The stable
# interval catches validators that were already being spawned when cleanup ran.
pkill -TERM -f "$NODE_PATTERN" || true

for _ in {1..100}; do
  if ! pgrep -f "$NODE_PATTERN" >/dev/null; then
    break
  fi
  sleep 0.1
done

# Force termination if graceful shutdown takes longer than 10 seconds
if pgrep -f "$NODE_PATTERN" >/dev/null; then
  echo "Aptos nodes did not stop after 10 seconds; forcing termination"
  pkill -KILL -f "$NODE_PATTERN" || true
  sleep 1
fi

# Keep killing late arrivals until no matching process has existed for two
# continuous seconds.
quiet_checks=0
for _ in {1..200}; do
  if pgrep -f "$NODE_PATTERN" >/dev/null; then
    pkill -KILL -f "$NODE_PATTERN" || true
    quiet_checks=0
  else
    quiet_checks=$((quiet_checks + 1))
    if (( quiet_checks >= 20 )); then
      break
    fi
  fi
  sleep 0.1
done

if pgrep -f "$NODE_PATTERN" >/dev/null; then
  echo "ERROR: Aptos node processes are still running for ${BASE_DIR}" >&2
  exit 1
fi

# Wait until every TCP port configured for the validators can be rebound. This
# covers sockets that outlive their process briefly and prevents the next test
# from failing with "Address already in use".
"$PYTHON_BIN" - "$BASE_DIR" <<'PY'
import re
import socket
import sys
import time
from pathlib import Path

import yaml


def address_port(value):
    if not value:
        return None
    match = re.search(r"(?:/tcp/|:)(\d+)(?:/|$)", str(value))
    return int(match.group(1)) if match else None


base_dir = Path(sys.argv[1])
ports = set()

for config_path in sorted((base_dir / "nodes").glob("*/node.yaml")):
    config = yaml.safe_load(config_path.read_text(encoding="utf-8")) or {}

    api = config.get("api") or {}
    inspection = config.get("inspection_service") or {}
    admin = config.get("admin_service") or {}
    storage = config.get("storage") or {}
    validator = config.get("validator_network") or {}

    candidates = (
        address_port(api.get("address")),
        inspection.get("port"),
        admin.get("port"),
        address_port(storage.get("backup_service_address")),
        address_port(validator.get("listen_address")),
    )
    ports.update(int(port) for port in candidates if port is not None)

deadline = time.monotonic() + 120
stable_since = None
busy_ports = []

while True:
    busy_ports = []
    for port in sorted(ports):
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        try:
            sock.bind(("0.0.0.0", port))
        except OSError:
            busy_ports.append(port)
        finally:
            sock.close()

    now = time.monotonic()
    if not busy_ports:
        stable_since = stable_since or now
        if now - stable_since >= 1.0:
            break
    else:
        stable_since = None

    if now >= deadline:
        print(
            f"ERROR: Aptos ports did not become reusable for {base_dir}: "
            + ", ".join(map(str, busy_ports)),
            file=sys.stderr,
        )
        raise SystemExit(1)

    time.sleep(0.1)
PY

# Wipe per-execution state while preserving generated node configurations.
rm -rf "${BASE_DIR}/nodes"/*/data || true
rm -f "${BASE_DIR}/nodes"/*/noise_secrets.jsonl || true
