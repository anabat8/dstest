SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APTOS_ROOT="${SCRIPT_DIR}/../../aptos-core"

cd "${APTOS_ROOT}"
cargo run --package aptos-framework
mkdir aptos-framework-release
cp aptos-framework/releases/artifacts/current/build/**/bytecode_modules/* aptos-framework-release