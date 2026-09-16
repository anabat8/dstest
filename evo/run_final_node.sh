#!/usr/bin/env bash

set -Eeuo pipefail

usage() {
    cat <<'EOF'
Usage: run_final_node.sh NODE_INDEX [--plan]

Run the final campaign queue assigned to one of the 15 CloudLab nodes.
NODE_INDEX must be an integer from 0 through 14. Use --plan to print the
assignment without preparing slots or starting any experiments.
EOF
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

NODE_INDEX="${1:-}"
MODE="${2:-}"

[[ "$NODE_INDEX" =~ ^([0-9]|1[0-4])$ ]] || {
    usage >&2
    exit 2
}
[[ -z "$MODE" || "$MODE" == "--plan" ]] || {
    usage >&2
    exit 2
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DSTEST_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PYTHON="$DSTEST_ROOT/.venv/bin/python"
CONFIG="$SCRIPT_DIR/configs/aptos_evo.yaml"
STATE_ROOT="${FINAL_STATE_ROOT:-/mydata/final_state/node${NODE_INDEX}}"
ARCHIVE_ROOT="${FINAL_ARCHIVE_ROOT:-/mydata/final_archives/node${NODE_INDEX}}"
EXPECTED_TESTS=1000
EXPECTED_WORKERS=4
EXPECTED_SLOTS=4
EXPECTED_STRIDE=7000
EXPECTED_PREFIX="-evo"

case "$NODE_INDEX" in
    0|1|2|3) BENCHMARK="aptos" ;;
    4|5|6|7) BENCHMARK="bug1" ;;
    8|9|10|11) BENCHMARK="bug2" ;;
    12|13|14) BENCHMARK="bug3" ;;
esac

case "$BENCHMARK" in
    aptos) BUG_FLAGS=(BUG1=false BUG2=false BUG3=false) ;;
    bug1)  BUG_FLAGS=(BUG1=true  BUG2=false BUG3=false) ;;
    bug2)  BUG_FLAGS=(BUG1=false BUG2=true  BUG3=false) ;;
    bug3)  BUG_FLAGS=(BUG1=false BUG2=false BUG3=true)  ;;
esac

# Entries are strategy|fitness|seed. Randomized runs report time_fitness, but
# result.json retains every fitness value for later cross-fitness comparisons.
case "$NODE_INDEX" in
    0|4|8)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|42"
            "evo_aptos|time_fitness|100"
            "evo_aptos|round_stress_fitness|2026"
            "evo_aptos|round_timeout_count_fitness|42"
            "evo_aptos|vote_fragmentation_fitness|100"
        )
        ;;
    1|5|9)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|100"
            "evo_aptos|time_fitness|2026"
            "evo_aptos|block_height_skew_fitness|42"
            "evo_aptos|round_timeout_count_fitness|100"
            "evo_aptos|vote_fragmentation_fitness|2026"
        )
        ;;
    2|6|10)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|2026"
            "evo_aptos|round_stress_fitness|42"
            "evo_aptos|block_height_skew_fitness|100"
            "evo_aptos|round_timeout_count_fitness|2026"
        )
        ;;
    3|7|11)
        CAMPAIGNS=(
            "evo_aptos|time_fitness|42"
            "evo_aptos|round_stress_fitness|100"
            "evo_aptos|block_height_skew_fitness|2026"
            "evo_aptos|vote_fragmentation_fitness|42"
        )
        ;;
    12)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|42"
            "evo_aptos|time_fitness|100"
            "evo_aptos|round_stress_fitness|2026"
            "evo_aptos|block_height_skew_fitness|42"
            "evo_aptos|round_timeout_count_fitness|100"
            "evo_aptos|vote_fragmentation_fitness|2026"
        )
        ;;
    13)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|100"
            "evo_aptos|time_fitness|2026"
            "evo_aptos|round_stress_fitness|42"
            "evo_aptos|block_height_skew_fitness|100"
            "evo_aptos|round_timeout_count_fitness|2026"
            "evo_aptos|vote_fragmentation_fitness|42"
        )
        ;;
    14)
        CAMPAIGNS=(
            "byzzfuzz|time_fitness|2026"
            "evo_aptos|time_fitness|42"
            "evo_aptos|round_stress_fitness|100"
            "evo_aptos|block_height_skew_fitness|2026"
            "evo_aptos|round_timeout_count_fitness|42"
            "evo_aptos|vote_fragmentation_fitness|100"
        )
        ;;
esac

print_plan() {
    echo "Node index: $NODE_INDEX"
    echo "Benchmark:  $BENCHMARK"
    echo "Campaigns:  ${#CAMPAIGNS[@]}"
    local entry strategy fitness seed
    for entry in "${CAMPAIGNS[@]}"; do
        IFS='|' read -r strategy fitness seed <<< "$entry"
        printf '  %-10s %-36s seed=%s\n' "$strategy" "$fitness" "$seed"
    done
}

print_plan
[[ "$MODE" == "--plan" ]] && exit 0

[[ -x "$PYTHON" ]] || die "missing Python environment: $PYTHON (run 'make setup' first)"
[[ -f "$CONFIG" ]] || die "missing config: $CONFIG"
command -v make >/dev/null || die "make is not installed"
command -v cargo >/dev/null || die "cargo is not on PATH (source ~/.cargo/env)"
command -v go >/dev/null || die "go is not on PATH"
command -v sha256sum >/dev/null || die "sha256sum is not installed"

read -r configured_workers configured_slots configured_stride configured_prefix \
    configured_tests configured_population configured_recovery < <(
    "$PYTHON" - "$CONFIG" <<'PY'
import sys
import yaml

with open(sys.argv[1], encoding="utf-8") as stream:
    config = yaml.safe_load(stream)

print(
    config["max_parallel_workers"],
    config["run_slots"],
    config["run_offset_stride"],
    config["run_tag_prefix"],
    config["total_num_tests"],
    config["population_size"],
    config["dstest"]["recovery_seconds"],
)
PY
)

[[ "$configured_workers" == "$EXPECTED_WORKERS" ]] || \
    die "max_parallel_workers must be $EXPECTED_WORKERS (found $configured_workers)"
[[ "$configured_slots" == "$EXPECTED_SLOTS" ]] || \
    die "run_slots must be $EXPECTED_SLOTS (found $configured_slots)"
[[ "$configured_stride" == "$EXPECTED_STRIDE" ]] || \
    die "run_offset_stride must be $EXPECTED_STRIDE (found $configured_stride)"
[[ "$configured_prefix" == "$EXPECTED_PREFIX" ]] || \
    die "run_tag_prefix must be $EXPECTED_PREFIX (found $configured_prefix)"
[[ "$configured_tests" == "$EXPECTED_TESTS" ]] || \
    die "total_num_tests must be $EXPECTED_TESTS (found $configured_tests)"
[[ "$configured_population" == "20" ]] || \
    die "population_size must be 20 (found $configured_population)"
[[ "$configured_recovery" == "60" ]] || \
    die "recovery_seconds must be 60 (found $configured_recovery)"

ephemeral_min="$(cut -f1 /proc/sys/net/ipv4/ip_local_port_range)"
[[ "$ephemeral_min" -gt 31040 ]] || \
    die "ephemeral port range starts at $ephemeral_min; final slot ports reach 31040"

mkdir -p "$STATE_ROOT" "$ARCHIVE_ROOT"

benchmark_file="$STATE_ROOT/benchmark"
if [[ -f "$benchmark_file" ]]; then
    [[ "$(<"$benchmark_file")" == "$BENCHMARK" ]] || \
        die "state directory belongs to benchmark $(<"$benchmark_file"), not $BENCHMARK"
else
    printf '%s\n' "$BENCHMARK" > "$benchmark_file"
fi

if [[ -f "$STATE_ROOT/node.complete" ]]; then
    echo "All assigned campaigns are already marked complete."
    exit 0
fi

if pgrep -af '[p]ython.*evo.py|[c]md/dstest/main.* run|[.]/main run' >/dev/null; then
    pgrep -af '[p]ython.*evo.py|[c]md/dstest/main.* run|[.]/main run' >&2 || true
    die "another campaign is active on this node"
fi

echo "Preparing four isolated runtime slots for $BENCHMARK..."
for slot in 0 1 2 3; do
    run_offset=$((slot * EXPECTED_STRIDE))
    make -C "$DSTEST_ROOT" \
        "${BUG_FLAGS[@]}" \
        RUN_TAG="-evo-s${slot}" \
        RUN_OFFSET="$run_offset" \
        NUM_REPLICAS=6 \
        NUM_CLIENT_ACCOUNTS=2 \
        seeds
done

git -C "$DSTEST_ROOT" rev-parse HEAD > "$STATE_ROOT/dstest_commit"
git -C "$DSTEST_ROOT/../aptos-core" rev-parse HEAD > "$STATE_ROOT/aptos_core_commit"
{
    date -Is
    rustc --version
    cargo --version
    go version
    uname -a
} > "$STATE_ROOT/environment.txt"

if [[ ! -f "$STATE_ROOT/summary.tsv" ]]; then
    printf 'campaign\tstrategy\tfitness\tseed\tresults\tfailures\tviolations\tunique_scheduler_seeds\trun_dir\tarchive\n' \
        > "$STATE_ROOT/summary.tsv"
fi

validate_campaign() {
    local run_dir="$1"
    local strategy="$2"
    "$PYTHON" - "$run_dir" "$strategy" "$EXPECTED_TESTS" <<'PY'
import json
import sys
from pathlib import Path

run_dir = Path(sys.argv[1])
strategy = sys.argv[2]
expected = int(sys.argv[3])
paths = sorted(run_dir.glob("**/result.json"))

failures = 0
violations = 0
scheduler_seeds = set()
test_indices = set()

for path in paths:
    with path.open(encoding="utf-8") as stream:
        result = json.load(stream)
    failures += int(bool(result.get("infrastructure_error")))
    violations += int(bool(result.get("violation")))
    scheduler_seeds.add(result.get("scheduler_seed"))
    test_indices.add(result.get("test_index"))

print(len(paths), failures, violations, len(scheduler_seeds), len(test_indices))

if len(paths) != expected:
    raise SystemExit(f"expected {expected} results, found {len(paths)}")
if failures:
    raise SystemExit(f"found {failures} infrastructure failures")
if len(test_indices) != expected or None in test_indices:
    raise SystemExit("test_index values are missing or duplicated")
if strategy == "byzzfuzz" and len(scheduler_seeds) != expected:
    raise SystemExit(
        f"randomized campaign has {len(scheduler_seeds)} unique scheduler seeds; "
        f"expected {expected}"
    )
PY
}

archive_campaign() {
    local run_dir="$1"
    local archive="$2"
    local run_name tmp_archive
    run_name="$(basename "$run_dir")"
    tmp_archive="${archive}.tmp.$$"

    if [[ -f "$archive" ]]; then
        echo "Archive already exists; verifying it: $archive"
        (cd "$(dirname "$archive")" && sha256sum -c "$(basename "$archive").sha256")
        return
    fi

    echo "Archiving $run_dir -> $archive"
    tar -czf "$tmp_archive" -C "$SCRIPT_DIR/logs" "$run_name"
    mv "$tmp_archive" "$archive"
    (
        cd "$(dirname "$archive")"
        sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256"
        sha256sum -c "$(basename "$archive").sha256"
    )
}

for entry in "${CAMPAIGNS[@]}"; do
    IFS='|' read -r strategy fitness seed <<< "$entry"
    campaign="${BENCHMARK}_${strategy}_${fitness}_seed${seed}_1000"
    done_file="$STATE_ROOT/${campaign}.done"
    run_file="$STATE_ROOT/${campaign}.run"
    console_log="$STATE_ROOT/${campaign}.console.log"
    archive="$ARCHIVE_ROOT/${campaign}.tar.gz"

    if [[ -f "$done_file" ]]; then
        echo "Skipping completed campaign: $campaign"
        continue
    fi

    if [[ ! -f "$run_file" ]]; then
        echo
        echo "===== Starting $campaign at $(date -Is) ====="
        before="$(find "$SCRIPT_DIR/logs" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' 2>/dev/null | sort -nr | head -1 | cut -d' ' -f2- || true)"

        set +e
        "$PYTHON" "$SCRIPT_DIR/evo.py" \
            --strategy "$strategy" \
            --benchmark "$BENCHMARK" \
            --fitness "$fitness" \
            --seed "$seed" \
            --total-num-tests "$EXPECTED_TESTS" \
            --max-parallel-workers "$EXPECTED_WORKERS" \
            --run-slots "$EXPECTED_SLOTS" \
            2>&1 | tee "$console_log"
        command_status="${PIPESTATUS[0]}"
        set -e

        [[ "$command_status" == "0" ]] || \
            die "$campaign exited with status $command_status; see $console_log"

        after="$(find "$SCRIPT_DIR/logs" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)"
        [[ -n "$after" && "$after" != "$before" ]] || \
            die "could not identify a new log directory for $campaign"
        readlink -f "$after" > "$run_file"
    fi

    run_dir="$(<"$run_file")"
    [[ -d "$run_dir" ]] || die "recorded run directory is missing: $run_dir"

    echo "Validating $campaign..."
    summary="$(validate_campaign "$run_dir" "$strategy")" || \
        die "$campaign failed validation; its files were retained at $run_dir"
    read -r results failures violations unique_seeds unique_indices <<< "$summary"

    archive_campaign "$run_dir" "$archive"

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$campaign" "$strategy" "$fitness" "$seed" "$results" "$failures" \
        "$violations" "$unique_seeds" "$run_dir" "$archive" \
        >> "$STATE_ROOT/summary.tsv"
    date -Is > "$done_file"
    echo "===== Completed $campaign: results=$results failures=$failures violations=$violations ====="
done

echo "All assigned campaigns completed. Cleaning runtime slots..."
for slot in 0 1 2 3; do
    make -C "$DSTEST_ROOT" \
        RUN_TAG="-evo-s${slot}" \
        RUN_OFFSET="$((slot * EXPECTED_STRIDE))" \
        clean
done

date -Is > "$STATE_ROOT/node.complete"
echo "Node $NODE_INDEX is complete. Archives: $ARCHIVE_ROOT"
