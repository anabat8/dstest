#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="${1:-/mydata/aptos-dstest}"
DSTEST_ROOT="$ROOT/dstest"
APTOS_CORE="$ROOT/aptos-core"
GO_VERSION="1.24.0"
GO_ARCHIVE="go${GO_VERSION}.linux-amd64.tar.gz"
GO_SHA256="dea9ca38a0b852a74e81c26134671af7c0fbe65d81b0dc1c5bfe22cf7d4c8858"

[[ -d "$ROOT/.git" ]] || {
    echo "ERROR: repository not found at $ROOT" >&2
    exit 1
}
[[ -d "$DSTEST_ROOT" && -d "$APTOS_CORE" ]] || {
    echo "ERROR: required submodules are missing; clone with --recurse-submodules" >&2
    exit 1
}

sudo apt-get update
sudo apt-get install -y \
    ca-certificates \
    curl \
    git \
    python3-venv \
    rsync \
    tmux

if [[ ! -x /usr/local/go/bin/go ]] || \
   [[ "$(/usr/local/go/bin/go version 2>/dev/null || true)" != "go version go${GO_VERSION} linux/amd64" ]]; then
    if [[ -e /usr/local/go ]]; then
        echo "ERROR: /usr/local/go exists but is not Go $GO_VERSION; inspect it before replacing it" >&2
        exit 1
    fi

    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT
    curl --fail --location --retry 3 \
        "https://go.dev/dl/${GO_ARCHIVE}" \
        --output "$tmp_dir/$GO_ARCHIVE"
    echo "$GO_SHA256  $tmp_dir/$GO_ARCHIVE" | sha256sum --check --status || {
        echo "ERROR: Go archive checksum did not match" >&2
        exit 1
    }
    sudo tar -C /usr/local -xzf "$tmp_dir/$GO_ARCHIVE"
fi

export PATH="/usr/local/go/bin:$HOME/.cargo/bin:$HOME/bin:$PATH"

if ! grep -Fqx 'export PATH="/usr/local/go/bin:$HOME/.cargo/bin:$HOME/bin:$PATH"' "$HOME/.profile" 2>/dev/null; then
    echo 'export PATH="/usr/local/go/bin:$HOME/.cargo/bin:$HOME/bin:$PATH"' >> "$HOME/.profile"
fi

cd "$APTOS_CORE"
./scripts/dev_setup.sh -b -t -k

export PATH="/usr/local/go/bin:$HOME/.cargo/bin:$HOME/bin:$PATH"
cd "$DSTEST_ROOT"
make setup

echo
echo "=== Final node bootstrap check ==="
printf 'parent:     %s\n' "$(git -C "$ROOT" rev-parse HEAD)"
printf 'dstest:     %s\n' "$(git -C "$DSTEST_ROOT" rev-parse HEAD)"
printf 'aptos-core: %s\n' "$(git -C "$APTOS_CORE" rev-parse HEAD)"
(
    cd "$APTOS_CORE"
    rustc --version
    cargo --version
)
go version
"$DSTEST_ROOT/.venv/bin/python" --version

[[ "$(go version)" == "go version go${GO_VERSION} linux/amd64" ]] || {
    echo "ERROR: unexpected Go version" >&2
    exit 1
}
(
    cd "$APTOS_CORE"
    [[ "$(rustc --version)" == rustc\ 1.90.0* ]]
) || {
    echo "ERROR: aptos-core did not select Rust 1.90.0" >&2
    exit 1
}

echo "Bootstrap complete. This node is ready for run_final_node.sh."
