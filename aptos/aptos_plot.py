#!/usr/bin/env python3
"""
Walks a dstest run directory and emits 2 plots:

    1_violations_over_time.png   cumulative agreement / liveness violations over
                                  experiment time (X labelled mm:ss).
    2_per_iter_overview.png      per-iter overview: client-tx commits, agreement
                                  & liveness violations and the
                                  iter's max block height.

Both plots carry the ByzzFuzz parameters (c, d, r, seed) and run-shape vars
(scheduler steps, iter count) in the title.

Usage:
    aptos_plot.py --run output/aptos/<RUN_ID>
                  [--config output/aptos/<RUN_ID>/aptos.yml]
                  [--out output/aptos/<RUN_ID>/plots]

The --config flag is optional; without it the plots show no params in the title.
"""

from __future__ import annotations

import argparse
import glob
import re
from dataclasses import dataclass
from pathlib import Path

import matplotlib.pyplot as plt
import matplotlib.ticker as mticker
import pandas as pd
import yaml

COL_COMMITS = "#1b9e77"  # green
COL_AG_VIOLS = "#b2182b" # red
COL_LV_VIOLS = "#d95f02" # orange
COL_HEIGHT = "#2166ac"   # blue

@dataclass
class IterStats:
    index: int
    commits: int = 0       # count of "committed tx=" across client_stdout_*.log
    ag_viols: int = 0      # "Disagreement detected ..." lines in agreement.log
    lv_viols: int = 0      # "Liveness failure: ..."     lines in agreement.log
    max_height: float = 0  # peak block height observed in the iter
    start_sec: float = 0   # first block log_time
    end_sec: float = 0     # last block log_time


@dataclass
class RunConfig:
    name: str = ""
    sched_type: str = ""
    c: int = 0
    d: int = 0
    r: int = 0
    seed: int = 0
    iterations: int = 0
    steps: int = 0


ITER_DIR_RE = re.compile(r"_(\d+)$")


def load_iters(run_dir: Path) -> list[IterStats]:
    out: list[IterStats] = []
    for child in run_dir.iterdir():
        if not child.is_dir():
            continue
        m = ITER_DIR_RE.search(child.name)
        if not m:
            continue
        s = IterStats(index=int(m.group(1)))
        s.commits = count_matches_glob(child / "client_stdout_*.log", "committed tx=")
        s.ag_viols, s.lv_viols = count_agreement_violations(child / "agreement.log")
        s.max_height, s.start_sec, s.end_sec = load_block_summary(child / "blockCommits.csv")
        # Skip empty iter dirs with no collectable data
        if s.start_sec == 0:
            continue
        out.append(s)
    out.sort(key=lambda x: x.index)
    return out


def load_block_summary(path: Path) -> tuple[float, float, float]:
    """
    Return (max_height, first_log_time_sec, last_log_time_sec) from blockCommits.csv
    """
    try:
        df = pd.read_csv(path)
    except (FileNotFoundError, pd.errors.EmptyDataError):
        return 0.0, 0.0, 0.0
    if df.empty or "log_time" not in df or "height" not in df:
        return 0.0, 0.0, 0.0
    return (
        float(df["height"].max()),
        df["log_time"].min() / 1e6,
        df["log_time"].max() / 1e6,
    )


def count_agreement_violations(path: Path) -> tuple[int, int]:
    """
    Split agreement.log into the two violation kinds the aptos agreement monitor
    writes (one line per event):
      - "Disagreement detected at height X between node A and node B"
      - "Liveness failure: no new blocks committed in N seconds"
    """
    try:
        text = path.read_text()
    except FileNotFoundError:
        return 0, 0
    return text.count("Disagreement detected"), text.count("Liveness failure")


def count_matches_glob(pattern: Path, needle: str) -> int:
    total = 0
    for p in glob.glob(str(pattern)):
        try:
            total += Path(p).read_text().count(needle)
        except OSError:
            continue
    return total


def load_config(path: Path) -> RunConfig:
    cfg = RunConfig()
    y = yaml.safe_load(path.read_text()) or {}
    tc = y.get("TestConfig", {}) or {}
    sc = y.get("SchedulerConfig", {}) or {}
    params = sc.get("Params", {}) or {}
    cfg.name = tc.get("Name", "") or ""
    cfg.iterations = int(tc.get("Iterations", 0) or 0)
    cfg.sched_type = sc.get("Type", "") or ""
    cfg.steps = int(sc.get("Steps", 0) or 0)
    cfg.seed = int(sc.get("Seed", 0) or 0)
    cfg.c = int(params.get("c", 0) or 0)
    cfg.d = int(params.get("d", 0) or 0)
    cfg.r = int(params.get("r", 0) or 0)
    return cfg


def params_subtitle(cfg: RunConfig) -> str:
    parts: list[str] = []
    if cfg.name:
        parts.append(cfg.name)
    if cfg.sched_type:
        parts.append(f"sched={cfg.sched_type}")
    if cfg.c or cfg.d or cfg.r:
        parts.append(f"c={cfg.c} d={cfg.d} r={cfg.r}")
    if cfg.steps:
        parts.append(f"steps={cfg.steps}")
    if cfg.iterations:
        parts.append(f"iters={cfg.iterations}")
    if cfg.seed:
        parts.append(f"seed={cfg.seed}")
    return "  |  ".join(parts)


def mmss(seconds: float) -> str:
    m, s = divmod(int(seconds), 60)
    return f"{m}m {s:02d}s"


# ---------------------------------------------------------------------
# Plot 1: cumulative violations vs experiment time
# ---------------------------------------------------------------------

def plot_violations_over_time(iters: list[IterStats], cfg: RunConfig, out_path: Path) -> None:
    start_times = [it.start_sec for it in iters if it.start_sec > 0]
    if not start_times:
        raise RuntimeError("no block timestamps")
    t0 = min(start_times)

    def series(get):
        xs, ys = [0.0], [0]
        cum = 0
        for it in iters:
            if it.end_sec == 0:
                continue
            cum += get(it)
            xs.append(it.end_sec - t0)
            ys.append(cum)
        return xs, ys

    fig, ax = plt.subplots(figsize=(12, 5))
    ax.step(*series(lambda it: it.ag_viols), where="post",
            color=COL_AG_VIOLS, lw=2, label="agreement violations")
    ax.step(*series(lambda it: it.lv_viols), where="post",
            color=COL_LV_VIOLS, lw=2, label="liveness violations")
    ax.set_xlabel("Experiment time (mm:ss)")
    ax.set_ylabel("Cumulative count")
    ax.set_title("Cumulative agreement / liveness violations\n" + params_subtitle(cfg))
    ax.xaxis.set_major_formatter(mticker.FuncFormatter(lambda v, _: mmss(v)))
    ax.legend(loc="upper left")
    ax.grid(True, alpha=0.3)
    fig.tight_layout()
    fig.savefig(out_path, dpi=120)
    plt.close(fig)


# ---------------------------------------------------------------------
# Plot 2: per-iter overview
# ---------------------------------------------------------------------

def plot_per_iter_overview(iters: list[IterStats], cfg: RunConfig, out_path: Path) -> None:
    n = len(iters)
    xs = list(range(n))
    labels = [str(it.index) for it in iters]
    commits = [it.commits for it in iters]
    ag = [it.ag_viols for it in iters]
    lv = [it.lv_viols for it in iters]
    heights = [it.max_height for it in iters]
    y_max = max([*commits, *ag, *lv, *heights], default=0)

    fig, ax = plt.subplots(figsize=(12, 5))
    bar_w = 0.8 / 3
    offsets = [-bar_w, 0.0, bar_w]
    ax.bar([x + offsets[0] for x in xs], commits, bar_w,
           color=COL_COMMITS, edgecolor=COL_COMMITS, linewidth=1,
           label="client tx commits")
    ax.bar([x + offsets[1] for x in xs], ag, bar_w,
           color=COL_AG_VIOLS, edgecolor=COL_AG_VIOLS, linewidth=1,
           label="agreement violations")
    ax.bar([x + offsets[2] for x in xs], lv, bar_w,
           color=COL_LV_VIOLS, edgecolor=COL_LV_VIOLS, linewidth=1,
           label="liveness violations")
    ax.plot(xs, heights, marker="o", color=COL_HEIGHT, lw=2, ms=5,
            label="max block height")

    ax.set_xticks(xs)
    ax.set_xticklabels(labels)
    ax.set_xlabel("Iteration")
    ax.set_ylabel("Block height")
    ax.set_title("Per-iter overview\n" + params_subtitle(cfg))
    ax.set_ylim(0, y_max * 1.4 if y_max > 0 else 1)
    ax.legend(loc="upper left")
    ax.grid(True, axis="y", alpha=0.3)
    fig.tight_layout()
    fig.savefig(out_path, dpi=120)
    plt.close(fig)


def main() -> None:
    ap = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    ap.add_argument("--run", required=True, type=Path,
                    help="Run directory (e.g. output/aptos/<RUN_ID>)")
    ap.add_argument("--config", type=Path,
                    help="Path to a dstest aptos*.yml for legend metadata (optional)")
    ap.add_argument("--out", type=Path,
                    help="Output dir for PNGs (default: <runDir>/plots)")
    args = ap.parse_args()

    out_dir = args.out or (args.run / "plots")
    out_dir.mkdir(parents=True, exist_ok=True)

    cfg = RunConfig()
    if args.config:
        try:
            cfg = load_config(args.config)
        except Exception as e:
            print(f"config load (continuing without params): {e}")

    iters = load_iters(args.run)
    if not iters:
        raise SystemExit(f"no iter dirs found under {args.run}")

    p1 = out_dir / "1_violations_over_time.png"
    plot_violations_over_time(iters, cfg, p1)
    print(f"wrote {p1}")

    p2 = out_dir / "2_per_iter_overview.png"
    plot_per_iter_overview(iters, cfg, p2)
    print(f"wrote {p2}")


if __name__ == "__main__":
    main()
