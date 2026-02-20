#!/usr/bin/env python3
import argparse
import json
import re
import sys
import os
from pathlib import Path
from typing import Dict, Any, Optional, Iterable, Tuple

# --------------------------------------------------------------------
# Step 0: Define parsing and filtering primitives (regex + keywords)
# --------------------------------------------------------------------
# LINE_RE:
# Matches a full Aptos log line and extracts structured fields.
#
# Example line:
# 2026-02-17T11:09:41.177185Z [consensus-4] INFO consensus/...:1023 Message
#
# Captured groups:
#   ts          -> ISO-8601 UTC timestamp
#   thr         -> logging thread / component (e.g. consensus-4)
#   level       -> log level (INFO, DEBUG, WARN, ...)
#   target      -> source file / module path
#   line line   -> source code line number
#   msg         -> remaining message content
#
# This lets us treat logs as structured events.
# ------------------------------------------------------------------

LINE_RE = re.compile(
    r"^(?P<ts>\d{4}-\d{2}-\d{2}T[0-9:.]+Z)\s+"
    r"\[(?P<thr>[^\]]+)\]\s+"
    r"(?P<level>[A-Z]+)\s+"
    r"(?P<target>\S+?):(?P<line>\d+)\s+"
    r"(?P<msg>.*)$"
)

# ------------------------------------------------------------------
# JSON_RE:
# Some Aptos log messages embed structured JSON payloads, e.g.
#   {"event":"NewRound","epoch":2,"round":51}
#
# This regex extracts the first {...} block so we can json.loads()
# it and access fields like event, epoch, round programmatically.
# ------------------------------------------------------------------

JSON_RE = re.compile(r"(\{.*\})")

# ------------------------------------------------------------------
# We keep a log line if:
#   - it contains a JSON payload with payload["event"] matching our keywords
#   - the raw message string contains any of our keywords
# ------------------------------------------------------------------

EVENT_KEYWORDS = [
    "NewEpoch", "NewRound", "CommitViaBlock",
    "Timeout", "RoundTimeout", "ReceiveRoundTimeout",
    "Vote", "VoteNIL",
    "Propose", "OptPropose",
    "ReceiveProposal", "ReceiveOptProposal", "ProcessOptProposal",
    "ReceiveSyncInfo", "SyncInfo", "ReceiveNewCertificate",
    "NetworkReceiveProposal", "NetworkReceiveOptProposal", "NetworkReceiveSyncInfo",
    "Broadcast", "BroadcastOrderVote", "BroadcastRandShareFastPath",
    "ReceiveOrderVote", "OrderVote", "ReceiveVote",
    "Signed ledger info", "Receive commit vote", "Receive ordered block",
    "leader", "Leader", "proposer", "Proposer", "proposer_election", "rotating_proposer",
    "pacemaker", "Pacemaker",
]
KW_RE = re.compile("|".join(re.escape(k) for k in EVENT_KEYWORDS))

# Infer node id from dsTest stdout_i.log / stderr_i.log filenames
NODE_FROM_FILENAME_RE = re.compile(r"^(stdout|stderr)_(\d+)\.log$", re.IGNORECASE)

# ------------------------------------------------------------------
# Helpers for detecting the experiment run directory
# ------------------------------------------------------------------
def looks_like_run_dir(d: Path) -> bool:
    """
    A 'run dir' is any directory that contains stdout_i.log / stderr_i.log files
    somewhere inside (dsTest output structure).
    """
    for f in d.rglob("*"):
        if f.is_file() and NODE_FROM_FILENAME_RE.match(f.name):
            return True
    return False

def expected_run_prefix() -> Optional[str]:
    """
    If provided, build the expected dsTest run dir prefix from env vars.
    Typical dsTest naming: <TestName>_<SchedulerType>_
    e.g. aptos-localnet_pct_*
    """
    test_name = os.getenv("TEST_NAME")
    sched_type = os.getenv("SCHED_TYPE")
    if test_name and sched_type:
        return f"{test_name}_{sched_type}_"
    return None

def find_run_dirs(run_id_dir: Path) -> list[Path]:
    """
    Prefer directories matching env vars (TEST_NAME + SCHED_TYPE),
    otherwise autodetect.
    """
    candidates = sorted([p for p in run_id_dir.iterdir() if p.is_dir()])

    pref = expected_run_prefix()
    hinted = []
    if pref:
        hinted = [d for d in candidates if d.name.startswith(pref)]
        # Keep only those that actually contain stdout_i/stderr_i logs
        hinted = [d for d in hinted if looks_like_run_dir(d)]
        if hinted:
            return hinted

    # autodetect
    autodetected = [d for d in candidates if looks_like_run_dir(d)]
    if autodetected:
        return autodetected

    # Last fallback: logs might be directly under RUN_ID dir
    if looks_like_run_dir(run_id_dir):
        return [run_id_dir]

    return []

# ------------------------------------------------------------------------------
# Step 1: Parse a single line into a structured record, and decide to keep/drop
# ------------------------------------------------------------------------------
def parse_line(line: str) -> Optional[Dict[str, Any]]:
    # Parses the log prefix into ts/thread/level/target/line/msg
    m = LINE_RE.match(line.rstrip("\n"))
    if not m:
        # Not an Aptos formatted line, drop it
        return None

    d = m.groupdict()
    msg = d["msg"]

    # Try to extract and parse an embedded JSON payload
    payload = None
    jm = JSON_RE.search(msg)
    if jm:
        blob = jm.group(1)
        try:
            payload = json.loads(blob)
        except json.JSONDecodeError:
            payload = None

    # Decide whether to keep this line
    # If payload has an event field, match it against the predefined keywords
    # Else, match the raw message text
    keep = False
    event = None
    
    if isinstance(payload, dict) and "event" in payload:
        event = payload.get("event")
        if isinstance(event, str) and KW_RE.search(event):
            keep = True

    if not keep and KW_RE.search(msg):
        keep = True

    if not keep:
        # Drop
        return None

    # Build a structured output record (normalized representation)
    out: Dict[str, Any] = {
        "ts": d["ts"],
        "thread": d["thr"],
        "level": d["level"],
        "target": d["target"],
        "line": int(d["line"]),
        "msg": msg,
    }

    # Attach JSON payload and convenient extracted fields if available
    if payload is not None:
        out["json"] = payload
        if event is None and isinstance(payload, dict):
            event = payload.get("event")
    if isinstance(event, str):
        out["event"] = event

    # Common fields, important for consensus
    if isinstance(payload, dict):
        for k in ("epoch", "round", "reason", "remote_peer", "block_round", "block_epoch", "block_author"):
            if k in payload:
                out[k] = payload[k]

    return out

# ------------------------------------------------------------------------------
# Step 2: Discover log files under the run directory (stdout_i.log/stderr_i.log)
# ------------------------------------------------------------------------------
def iter_log_files(root: Path) -> Iterable[Tuple[Path, Optional[int]]]:
    """
      - Recursively walk 'root'
      - Keep only files named stdout_<i>.log or stderr_<i>.log
      - Yield (file_path, node_id)
    """
    for f in root.rglob("*"):
        if not f.is_file():
            continue
        m = NODE_FROM_FILENAME_RE.match(f.name)
        if not m:
            continue
        node_id = int(m.group(2))
        yield f, node_id

# ------------------------------------------------------------------------------
# Step 3: Write the records (JSONL + pretty log text)
# ------------------------------------------------------------------------------
def write_record(jf, tf, rec: Dict[str, Any], source_file: Path, run_name: str, node_id: int) -> None:
    """
      - Enrich record with provenance (which file, which event scheduler run dir, which node)
      - Write JSONL
      - Write pretty text line
    """
    
    rec = dict(rec)  # copy so we don't mutate the original
    rec["source_file"] = str(source_file)
    rec["run_dir"] = run_name
    rec["node"] = node_id

    # JSONL output (one JSON object per line)
    jf.write(json.dumps(rec, ensure_ascii=False) + "\n")

    # Use whole text:
    # ev = rec.get("event") or ""
    # tf.write(f'{rec["ts"]} [node{node_id}] [{rec["thread"]}] {rec["level"]} {ev} {rec["msg"]}\n')
    # Pretty text: timestamp + original message only
    tf.write(f'{rec["ts"]} {rec["msg"]}\n')

# ------------------------------------------------------------------------------------
# Step 4: Filter one scheduler-iteration-run directory (per node outputs and combined outputs)
# ------------------------------------------------------------------------------------
def filter_one_run_dir(run_dir: Path, out_dir: Path, level_set: Optional[set]) -> Tuple[int, int]:
    """
    Returns (scanned_lines, kept_lines)
    """
    run_name = run_dir.name  # e.g. aptos-localnet_pct_0
    out_dir.mkdir(parents=True, exist_ok=True)

    # One writer per node (jsonl + text)
    writers: Dict[int, Tuple[Any, Any]] = {}
    
    def get_writers(node_id: int):
        if node_id not in writers:
            jf = (out_dir / f"node{node_id}.jsonl").open("w", encoding="utf-8")
            tf = (out_dir / f"node{node_id}.log").open("w", encoding="utf-8")
            writers[node_id] = (jf, tf)
        return writers[node_id]

    # Also create combined outputs across all nodes
    all_jf = (out_dir / "all_nodes.jsonl").open("w", encoding="utf-8")
    all_tf = (out_dir / "all_nodes.log").open("w", encoding="utf-8")

    scanned = 0
    kept = 0

    # Iterate log files and stream lines
    for f, node_id in iter_log_files(run_dir):
        try:
            with f.open("r", encoding="utf-8", errors="replace") as fh:
                for line in fh:
                    scanned += 1
                    
                    # Parse + filter by consensus relevance
                    rec = parse_line(line)
                    if rec is None:
                        continue
                    
                    # Optional log-level filtering (INFO/DEBUG/etc)
                    if level_set and rec["level"] not in level_set:
                        continue
                    
                    # Write per-node output
                    node_jf, node_tf = get_writers(node_id)
                    write_record(node_jf, node_tf, rec, f, run_name, node_id)
                    
                    # Write combined output
                    write_record(all_jf, all_tf, rec, f, run_name, node_id)
                    
                    kept += 1
        except OSError:
            continue

    # close everything
    for jf, tf in writers.values():
        jf.close()
        tf.close()
    all_jf.close()
    all_tf.close()

    return scanned, kept


def main() -> None:
    ap = argparse.ArgumentParser(description="Filter Aptos consensus logs per dsTest scheduler run and per node.")
    ap.add_argument("run_dir", help="The RUN_ID directory (e.g., output/aptos/20260217_120010)")
    ap.add_argument("--levels", default=None,
                    help="Comma-separated levels to keep (e.g. INFO,DEBUG). If unset, keep all.")
    ap.add_argument("--out-subdir", default="filtered", help="Name of folder created inside each scheduler-run dir.")
    args = ap.parse_args()

    level_set = None
    if args.levels:
        level_set = {x.strip().upper() for x in args.levels.split(",") if x.strip()}

    run_id_dir = Path(args.run_dir)
    if not run_id_dir.is_dir():
        print(f"ERROR: not a directory: {run_id_dir}", file=sys.stderr)
        sys.exit(2)

    # Find the run directories
    run_dirs = find_run_dirs(run_id_dir)

    if not run_dirs:
        pref = expected_run_prefix()
        if pref:
            print(
                f"No run directories found under {run_id_dir} "
                f"matching prefix '{pref}', and autodetection found none either.",
                file=sys.stderr,
            )
        else:
            print(
                f"No scheduler run directories found under {run_id_dir}",
                file=sys.stderr,
            )

    # Process each run directory
    total_scanned = 0
    total_kept = 0

    for rd in run_dirs:
        out_dir = rd / args.out_subdir
        scanned, kept = filter_one_run_dir(rd, out_dir, level_set)

        total_scanned += scanned
        total_kept += kept

        print(
            f"[{rd.name}] scanned={scanned} kept={kept} -> {out_dir}",
            file=sys.stderr,
        )
    
    print(f"TOTAL scanned={total_scanned} kept={total_kept}", file=sys.stderr)


if __name__ == "__main__":
    main()