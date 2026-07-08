import json
import math
import argparse
import os
from pathlib import Path
import re

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import seaborn as sns
from sklearn.metrics import roc_auc_score
import pandas as pd

from fitness import ITER_DIR_RE, TestFitness, ViolationParser

"""
1. Boxplots: buggy vs unbuggy fitness distributions
2. Evolution curves: mean/max fitness and violation rate per generation
3. Top-K discovery: how quickly high-fitness executions reveal bugs
4. AUC bar chart: compact comparison between fitness functions
"""

def load_evo_results(log_root):
    rows = []

    for path in Path(log_root).glob("**/result.json"):
        data = json.loads(path.read_text())

        m = re.match(r"G(\d+)T(\d+)", path.parent.name)
        if not m:
            continue

        if data.get("infrastructure_error") or data.get("fitness", -1000) == -1000:
            continue

        generation = int(m.group(1))
        individual_id = int(m.group(2))
        fitness_dir = path.parents[1].name
        strategy = path.parents[2].name
        benchmark = path.parents[3].name

        row = {
            "generation": generation,
            "individual_id": individual_id,
            "iteration": None,
            "benchmark": benchmark,
            "strategy": strategy,
            "fitness_dir": fitness_dir,
            "optimized_fitness": data.get("fitness_name"),
            "source_format": "evo",
            "source_path": str(path.parent),
            "buggy": bool(data.get("buggy", data.get("violation", 0))),
            "agreement": bool(data.get("agreement", False)),
            "liveness": bool(data.get("liveness", False)),
        }

        for name, value in data.get("fitness_values", {}).items():
            row[name] = value

        rows.append(row)

    return pd.DataFrame(rows)


def find_dstest_iter_dirs(run_root):
    run_root = Path(run_root)
    parser = ViolationParser([])

    if parser.is_populated_iter_dir(run_root):
        return [run_root]

    candidates = [
        child for child in run_root.iterdir()
        if child.is_dir()
        and ITER_DIR_RE.search(child.name)
        and parser.is_populated_iter_dir(child)
    ]
    if candidates:
        return sorted(candidates, key=lambda p: int(ITER_DIR_RE.search(p.name).group(1)))

    # Allow passing a higher-level output directory, e.g. dstest/output/aptos-b1-c1d1
    candidates = []
    for child in run_root.iterdir():
        if not child.is_dir():
            continue
        for grandchild in child.iterdir():
            if (
                grandchild.is_dir()
                and ITER_DIR_RE.search(grandchild.name)
                and parser.is_populated_iter_dir(grandchild)
            ):
                candidates.append(grandchild)

    return sorted(
        candidates,
        key=lambda p: (str(p.parent), int(ITER_DIR_RE.search(p.name).group(1))),
    )


def load_dstest_results(run_root, config):
    rows = []

    for iter_dir in find_dstest_iter_dirs(run_root):
        evaluation = TestFitness(iter_dir, config).evaluate()
        metrics = evaluation["metrics"]
        fitness_values = evaluation["fitness_values"]
        iter_match = ITER_DIR_RE.search(iter_dir.name)
        iteration = int(iter_match.group(1)) if iter_match else None

        row = {
            "generation": None,
            "individual_id": None,
            "iteration": iteration,
            "benchmark": iter_dir.parent.parent.name if iter_dir.parent.parent else "",
            "strategy": "byzzfuzz",
            "fitness_dir": None,
            "optimized_fitness": None,
            "source_format": "dstest",
            "source_path": str(iter_dir),
            "buggy": bool(evaluation["buggy"]),
            "agreement": bool(metrics.get("agreement_violations", 0)),
            "liveness": bool(metrics.get("real_liveness", 0)),
        }
        row.update(metrics)
        row.update(fitness_values)
        rows.append(row)

    return pd.DataFrame(rows)


def load_results(input_roots, config):
    rows = []

    for input_root in input_roots:
        input_root = Path(input_root)
        if not input_root.exists():
            print(f"Skipping missing input root: {input_root}")
            continue

        evo_df = load_evo_results(input_root)
        if not evo_df.empty:
            rows.append(evo_df)
            continue

        dstest_df = load_dstest_results(input_root, config)
        if not dstest_df.empty:
            rows.append(dstest_df)

    if not rows:
        return pd.DataFrame()

    return pd.concat(rows, ignore_index=True)


def plot_buggy_vs_unbuggy(df, fitness_names):
    fitness_names = [fitness for fitness in fitness_names if fitness in df.columns]
    if not fitness_names:
        return None

    fig, axes = plt.subplots(
        math.ceil(len(fitness_names) / 2),
        2,
        figsize=(12, 4 * math.ceil(len(fitness_names) / 2)),
        squeeze=False,
    )

    for ax, fitness in zip(axes.ravel(), fitness_names):
        sub = df.dropna(subset=[fitness]).copy()
        sub["label"] = sub["buggy"].map({False: "No violation", True: "Violation"})
        order = ["No violation", "Violation"]
        palette = {
            "No violation": "#76a9e0",
            "Violation": "#e88484",
        }

        sns.boxplot(
            data=sub,
            x="label",
            y=fitness,
            hue="label",
            order=order,
            hue_order=order,
            ax=ax,
            palette=palette,
            width=0.55,
            linewidth=1.4,
            showfliers=False,
            dodge=False,
            legend=False,
        )

        clean = sub[~sub["buggy"]]
        sns.stripplot(
            data=clean,
            x="label",
            y=fitness,
            order=order,
            ax=ax,
            color="#2f2f2f",
            alpha=0.22,
            size=2.5,
            jitter=0.20,
            zorder=2,
        )

        n_clean = (~sub["buggy"]).sum()
        n_buggy = sub["buggy"].sum()
        violation_values = sub.loc[sub["buggy"], fitness].tolist()
        for idx, value in enumerate(violation_values):
            offset = ((idx % 7) - 3) * 0.025
            ax.scatter(
                1 + offset,
                value,
                marker="D",
                s=58,
                color="#b2182b",
                edgecolors="black",
                linewidths=0.55,
                alpha=0.95,
                zorder=6,
            )
        if violation_values:
            sorted_values = sorted(violation_values)
            mid = len(sorted_values) // 2
            if len(sorted_values) % 2:
                violation_median = sorted_values[mid]
            else:
                violation_median = (sorted_values[mid - 1] + sorted_values[mid]) / 2
            ax.hlines(
                violation_median,
                0.78,
                1.22,
                colors="#b2182b",
                linewidth=2.2,
                zorder=5,
            )
        ax.set_title(fitness)
        ax.set_xticks([0, 1])
        ax.set_xticklabels([
            f"No violation\nn={n_clean}",
            f"Violation\nn={n_buggy}",
        ], rotation=0)
        ax.tick_params(axis="x", labelrotation=0)
        ax.set_xlabel("")
        ax.set_ylabel("Fitness")

    for ax in axes.ravel()[len(fitness_names):]:
        ax.axis("off")

    fig.tight_layout()
    return fig


def plot_evolution_curve(df, fitness):
    if fitness not in df.columns:
        return None

    sub = df.dropna(subset=[fitness]).copy()
    if sub.empty:
        return None

    grouped = sub.groupby("generation")
    stats = grouped.agg(
        fitness_mean=(fitness, "mean"),
        fitness_min=(fitness, "min"),
        fitness_max=(fitness, "max"),
        violation_rate=("buggy", "mean"),
    ).reset_index()

    fig, ax1 = plt.subplots(figsize=(9, 4))

    ax1.plot(
        stats["generation"],
        stats["fitness_mean"],
        marker="o",
        color="#e66b00",
        label="Fitness Mean",
    )
    ax1.plot(
        stats["generation"],
        stats["fitness_max"],
        linestyle="--",
        color="#e66b00",
        label="Fitness Max",
    )
    ax1.fill_between(
        stats["generation"],
        stats["fitness_min"],
        stats["fitness_max"],
        color="#e66b00",
        alpha=0.15,
    )

    ax1.set_xlabel("Generation")
    ax1.set_ylabel("Fitness")

    ax2 = ax1.twinx()
    ax2.plot(
        stats["generation"],
        stats["violation_rate"],
        marker="s",
        color="#169b82",
        label="Violation Rate",
    )
    ax2.set_ylabel("Violation Rate")
    ax2.set_ylim(-0.05, 1.05)

    ax1.set_title(f"{fitness} over generations")

    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, loc="best")

    fig.tight_layout()
    return fig


"""
For each fitness function:
    - Take all executions
    - Sort them by that fitness, descendingly
    - Walk through the sorted list from best score to worst score
    - Count how many buggy executions have been found so far
    
Good fitness:
  curve rises sharply above random baseline

Bad fitness:
  curve follows random baseline
  curve rises late, below random baseline
"""
def plot_topk_discovery(df, fitness_names):
    fitness_names = [fitness for fitness in fitness_names if fitness in df.columns]
    if not fitness_names:
        return None

    fig, ax = plt.subplots(figsize=(8, 5))

    for fitness in fitness_names:
        sub = df.dropna(subset=[fitness]).copy()
        if sub.empty:
            continue
        sub = sub.sort_values(fitness, ascending=False).reset_index(drop=True)

        y = sub["buggy"].astype(int).cumsum()
        x = range(1, len(sub) + 1)

        ax.plot(x, y, label=fitness)

        total = len(sub)
        total_buggy = sub["buggy"].sum()
        random_baseline = [total_buggy * (k / total) for k in range(1, total + 1)]
        ax.plot(
            range(1, total + 1),
            random_baseline,
            linestyle="--",
            alpha=0.35,
            label=f"{fitness} random baseline",
        )

    ax.set_xlabel("Executions inspected, sorted by descending fitness")
    ax.set_ylabel("Cumulative violations found")
    ax.set_title("Top-K violation discovery")
    ax.legend()
    fig.tight_layout()
    return fig


"""
AUC = probability that a random buggy execution has higher fitness than a random unbuggy execution

AUC = 1.0: perfect separation between buggy and unbuggy executions
AUC = 0.5: fitness is no better than random
AUC < 0.5: the fitness tends to score non-violations higher
AUC = 0.0: perfectly wrong
"""
def compute_auc(df, fitness):
    sub = df.dropna(subset=[fitness])
    if sub["buggy"].nunique() < 2:
        return float("nan")
    return roc_auc_score(sub["buggy"].astype(int), sub[fitness])


def compute_auc_scores(df, fitness_names):
    rows = []

    for fitness in fitness_names:
        if fitness not in df.columns:
            auc = float("nan")
        else:
            auc = compute_auc(df, fitness)

        rows.append({
            "fitness": fitness,
            "auc": auc,
        })

    return pd.DataFrame(rows)


def plot_auc_summary(df, fitness_names):
    auc_df = compute_auc_scores(df, fitness_names)
    auc_df = auc_df.dropna(subset=["auc"])
    if auc_df.empty:
        return None

    fig, ax = plt.subplots(figsize=(8, 4))
    sns.barplot(data=auc_df, x="fitness", y="auc", ax=ax, color="#76a9e0")
    ax.axhline(0.5, color="gray", linestyle="--", label="Random")
    ax.set_ylim(0, 1)
    ax.set_ylabel("AUC")
    ax.set_xlabel("")
    ax.set_title("Fitness quality: buggy vs unbuggy separation")
    ax.tick_params(axis="x", labelrotation=0)
    ax.legend()
    fig.tight_layout()
    return fig


# Helpers

def save_figure(fig, path):
    if fig is None:
        return
    fig.savefig(path, dpi=180, bbox_inches="tight")
    plt.close(fig)


def safe_name(value):
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", str(value)).strip("-")


def main():
    parser = argparse.ArgumentParser(
        description="Plot fitness behavior from evo result.json files and/or dsTest output iteration dirs.",
    )
    parser.add_argument(
        "input_roots",
        nargs="+",
        type=Path,
        help=(
            "One or more roots. Each can be an evo log tree with result.json files, "
            "a dsTest run dir with *_byzzfuzz_<iter> children, or a single iteration dir."
        ),
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Directory where plot PNGs are written. Defaults to <input_root>/plots for one root.",
    )
    parser.add_argument(
        "--num-nodes",
        type=int,
        default=6,
        help="Number of Aptos validators expected in blockCommits.csv for dsTest output roots.",
    )
    parser.add_argument(
        "--fitness",
        nargs="*",
        default=[
            "time_fitness",
            "round_stress_fitness",
            "block_height_skew_fitness",
        ],
        help="Fitness value names to plot.",
    )
    args = parser.parse_args()

    config = {"dstest": {"num_nodes": args.num_nodes}}
    df = load_results(args.input_roots, config)
    if df.empty:
        raise SystemExit(f"No usable evo or dsTest results found under: {args.input_roots}")

    fitness_names = [fitness for fitness in args.fitness if fitness in df.columns]
    if not fitness_names:
        available = sorted(col for col in df.columns if col.endswith("_fitness"))
        raise SystemExit(
            "None of the requested fitness columns exist. "
            f"Requested={args.fitness}; available={available}"
        )

    if args.out_dir is not None:
        out_dir = args.out_dir
    elif len(args.input_roots) == 1:
        out_dir = args.input_roots[0] / "plots"
    else:
        out_dir = Path("dstest/evo/plots/combined")

    out_dir.mkdir(parents=True, exist_ok=True)
    df.to_csv(out_dir / "plot_data.csv", index=False)

    save_figure(
        plot_buggy_vs_unbuggy(df, fitness_names),
        out_dir / "buggy_vs_unbuggy_distributions.png",
    )
    save_figure(
        plot_topk_discovery(df, fitness_names),
        out_dir / "topk_violation_discovery.png",
    )
    save_figure(
        plot_auc_summary(df, fitness_names),
        out_dir / "auc_summary.png",
    )

    for fitness in fitness_names:
        optimized = df[df["optimized_fitness"] == fitness]
        if optimized.empty:
            continue
        save_figure(
            plot_evolution_curve(optimized, fitness),
            out_dir / f"evolution_{safe_name(fitness)}.png",
        )

    print(f"Wrote plots to {out_dir}")


if __name__ == "__main__":
    main()
