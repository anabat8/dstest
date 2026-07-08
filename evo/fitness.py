import csv
import argparse
import json
import re
from pathlib import Path

ITER_DIR_RE = re.compile(r"_(\d+)$")

class FitnessFunction:
    name: str

    def evaluate(self, *args, **kwargs) -> float:
        raise NotImplementedError
    
    
    def num_nodes(self, config, rows):
        dstest_config = config.get("dstest", {}) or {}
        configured = dstest_config.get("num_nodes", config.get("num_nodes"))
        if configured is not None:
            return int(configured)

        node_ids = {row["node_id"] for row in rows}
        return len(node_ids)


    def read_block_commits(self, block_commits_path):
        try:
            with Path(block_commits_path).open(newline="", encoding="utf-8") as f:
                rows = list(csv.DictReader(f))
        except FileNotFoundError:
            return []

        valid_rows = []
        for row in rows:
            try:
                valid_rows.append({
                    "node_id": int(row["node_id"]),
                    "height": int(float(row["height"])),
                    "epoch": int(row["epoch"]),
                    "round": int(row["round"]),
                    "log_time": int(row["log_time"]),
                })
            except (KeyError, TypeError, ValueError):
                continue

        return sorted(valid_rows, key=lambda row: row["log_time"])


"""
This fitness function rewards executions in which it takes longer time to commit a new block.
"""
class TimeFitness(FitnessFunction):
    name = "time_fitness"

    def evaluate(self, block_commits_path, config) -> float:
        metrics = self.collect_metrics(block_commits_path, config)
        return metrics["max_inter_block_latency_sec"]


    def collect_metrics(self, block_commits_path, config):
        rows = self.read_block_commits(block_commits_path)
        num_nodes = self.num_nodes(config, rows)
        global_commit_times = self.global_commit_times(rows, num_nodes)
        latencies = self.inter_block_latencies(global_commit_times)
        
        max_latency = max(latencies, default=0.0)
        
        # if block heights did not increase, we have a stalled execution
        # boost these executions
        tail_latency = 0.0
        if rows and global_commit_times:
            last_global_commit_time = max(global_commit_times.values())
            last_observed_time = rows[-1]["log_time"]
            tail_latency = (last_observed_time - last_global_commit_time) / 1_000_000.0
            max_latency = max(max_latency, tail_latency)

        # normal run: fitness is the max time between consecutive globally committed blocks
        # stalled run: fitness can become the time since the last globally committed block
        return {
            "max_inter_block_latency_sec": max_latency,
            "avg_inter_block_latency_sec": (
                sum(latencies) / len(latencies) if latencies else 0.0
            ),
            "global_blocks_committed": len(global_commit_times),
        }


    def global_commit_times(self, rows, num_nodes):
        if not rows or num_nodes <= 0:
            return {}

        node_heights = {node_id: 0 for node_id in range(num_nodes)}
        commit_times = {}
        next_height = 1

        for row in rows:
            node_id = row["node_id"]
            if node_id not in node_heights:
                continue

            node_heights[node_id] = max(node_heights[node_id], row["height"])
            global_height = min(node_heights.values())

            while next_height <= global_height:
                commit_times[next_height] = row["log_time"]
                next_height += 1

        return commit_times


    def inter_block_latencies(self, global_commit_times):
        heights = sorted(global_commit_times)
        return [
            (global_commit_times[curr] - global_commit_times[prev]) / 1_000_000.0
            for prev, curr in zip(heights, heights[1:])
        ]


"""
For each observed globally committed height, we remember the Aptos consensus round of that block.
Then we compute how large the jumps are between consecutive committed block rounds.
This fitness function rewards executions in which the gaps between those are larger. 
Bigger round gaps mean: failed rounds, leader/proposal issues, timeouts.
"""
class RoundStressFitness(FitnessFunction):
    name = "round_stress_fitness"
    
    def evaluate(self, block_commits_path, config) -> float:
        metrics = self.collect_metrics(block_commits_path, config)
        return metrics["max_round_gap_between_committed_blocks"]

    
    def collect_metrics(self, block_commits_path, config):
        rows = self.read_block_commits(block_commits_path)
        num_nodes = self.num_nodes(config, rows)
        global_rounds = self.global_commit_rounds(rows, num_nodes)
        gaps = self.round_gaps(global_rounds)

        return {
            "max_round_gap_between_committed_blocks": max(gaps, default=0),
            "avg_round_gap_between_committed_blocks": (
                sum(gaps) / len(gaps) if gaps else 0.0
            ),
            "global_rounds_committed": len(global_rounds),
        }

    
    def global_commit_rounds(self, rows, num_nodes):
        if not rows or num_nodes <= 0:
            return {}
        
        node_heights = {node_id: 0 for node_id in range(num_nodes)}
        observed_rounds_by_height = {}      # store all (epoch, round) values observed for each height
        global_rounds = {}                  # store round for each globally committed height
        next_height = 1
        
        for row in rows:
            node_id = row["node_id"]
            if node_id not in node_heights:
                continue
            
            height = row["height"]
            observed_rounds_by_height.setdefault(height, []).append(
                (row["epoch"], row["round"])
            )
            
            node_heights[node_id] = max(node_heights[node_id], height)
            # a height is globally committed only when all nodes have reached at least that height
            global_height = min(node_heights.values())     
            
            while next_height <= global_height:
                observed = observed_rounds_by_height.get(next_height)
                if observed:
                    global_rounds[next_height] = max(observed)
                next_height += 1

        return global_rounds
    
    
    def round_gaps(self, global_rounds):
        gaps = []
        previous = None
        
        for height in sorted(global_rounds):
            epoch, round_nr = global_rounds[height]
            
            if previous is None:
                gaps.append(round_nr) # also reward a slow first commit
            else:
                prev_epoch, prev_round = previous
                if epoch == prev_epoch:
                    gaps.append(max(0, round_nr - prev_round))
                else:
                    gaps.append(round_nr)
                    
            previous = (epoch, round_nr)
            
        return gaps
        

"""
This fitness function rewards executions in which validators progress at very different block heights
(indicates that some validators are lagging behind).
"""
class BlockHeightSkewFitness(FitnessFunction):
    name = "block_height_skew_fitness"
    
    def evaluate(self, block_commits_path, config) -> float:
        metrics = self.collect_metrics(block_commits_path, config)
        return metrics["max_block_height_skew"]
    
    def collect_metrics(self, block_commits_path, config):
        rows = self.read_block_commits(block_commits_path)
        num_nodes = self.num_nodes(config, rows)
        
        if not rows or num_nodes <= 0:
            return {
                "max_block_height_skew": 0,
                "final_block_height_skew": 0,
            }
        
        node_heights = {node_id: 0 for node_id in range(num_nodes)}
        max_skew = 0
        
        for row in rows:
            node_id = row["node_id"]
            if node_id not in node_heights:
                continue
            
            height = row["height"]
            node_heights[node_id] = max(node_heights[node_id], height)
            skew = max(node_heights.values()) - min(node_heights.values())
            max_skew = max(max_skew, skew)
        
        final_skew = max(node_heights.values()) - min(node_heights.values())

        return {
            "max_block_height_skew": max_skew,
            "final_block_height_skew": final_skew,
        }


class TestFitness:
    def __init__(self, output_dir=None, config=None):
        self.fitnesses = [
            TimeFitness(),
            RoundStressFitness(),
            BlockHeightSkewFitness(),
        ]
        self.output_dir = Path(output_dir) if output_dir is not None else None
        self.config = config or {}
        self.iter_dir = None
    
    def collect_iter_dir(self):
        if self.output_dir is None or not self.output_dir.exists():
            self.iter_dir = None
            return None

        if (self.output_dir / "blockCommits.csv").exists():
            self.iter_dir = self.output_dir
            return self.iter_dir

        parser = ViolationParser([])
        iter_dirs = [
            p for p in self.output_dir.iterdir()
            if p.is_dir()
            and ITER_DIR_RE.search(p.name)
            and parser.is_populated_iter_dir(p)
        ]
        iter_dirs.sort(key=lambda p: int(ITER_DIR_RE.search(p.name).group(1)))

        if not iter_dirs:
            self.iter_dir = None
            return None

        self.iter_dir = iter_dirs[0]
        return self.iter_dir


    def evaluate(self, output_dir=None, config=None):
        if output_dir is not None:
            self.output_dir = Path(output_dir)
        if config is not None:
            self.config = config

        iter_dir = self.collect_iter_dir()
        if iter_dir is None:
            return {
                "fitness_values": {
                    "time_fitness": -1000.0,
                    "round_stress_fitness": -1000.0,
                    "block_height_skew_fitness": -1000.0,
                },
                "metrics": {
                    "commits": 0,
                    "agreement_violations": 0,
                    "real_liveness": 0,
                    "potential_liveness": 0,
                    "max_height": 0.0,
                    "mutations": 0,

                    "max_inter_block_latency_sec": 0.0,
                    "avg_inter_block_latency_sec": 0.0,
                    "global_blocks_committed": 0,

                    "max_round_gap_between_committed_blocks": 0,
                    "avg_round_gap_between_committed_blocks": 0.0,
                    "global_rounds_committed": 0,

                    "max_block_height_skew": 0,
                    "final_block_height_skew": 0,
                },
                "buggy": False,
            }

        v_parser = ViolationParser([iter_dir])
        metrics = v_parser.parse_run_output()

        time_fitness = TimeFitness()
        time_metrics = time_fitness.collect_metrics(
            iter_dir / "blockCommits.csv",
            self.config,
        )
        metrics.update(time_metrics)
        
        round_stress_fit = RoundStressFitness()
        round_stress_metrics = round_stress_fit.collect_metrics(
            iter_dir / "blockCommits.csv",
            self.config,
        )
        metrics.update(round_stress_metrics)
        
        height_skew = BlockHeightSkewFitness()
        height_skew_metrics = height_skew.collect_metrics(
            iter_dir / "blockCommits.csv",
            self.config,
        )
        metrics.update(height_skew_metrics)

        has_violation = (
            metrics["agreement_violations"] > 0
            or metrics["real_liveness"] > 0
        )
 
        return {
            "fitness_values": {
                "time_fitness": time_metrics["max_inter_block_latency_sec"],
                "round_stress_fitness": round_stress_metrics["max_round_gap_between_committed_blocks"],
                "block_height_skew_fitness": height_skew_metrics["max_block_height_skew"],
            },
            "buggy": has_violation,
            "metrics": metrics,
        }
    
    
# ************************************************* #
# 		  General Parser for 1 output run           #
# ************************************************* #
class ViolationParser:
    def __init__(self, iter_dirs):
        self.iter_dirs = iter_dirs
    
    
    def count_agreement(self, path):
        try:
            text = path.read_text(encoding="utf-8")
        except FileNotFoundError:
            return 0, 0, 0

        heights = {
            int(m.group(1))
            for m in re.finditer(r"Disagreement detected at height (\d+)", text)
        }
        real_liveness = int(bool(re.search(r"Liveness (failure|violation)", text)))
        potential_liveness = text.count("Potential liveness timeout")
        return len(heights), real_liveness, potential_liveness


    def is_populated_iter_dir(self, iter_dir):
        # A real iteration should at least have block commit rows.
        # This skips leftover dirs that only contain an empty/header-only mutations.csv.
        if self.csv_has_rows(iter_dir / "blockCommits.csv"):
            return True

        if (iter_dir / "agreement.log").exists():
            try:
                if (iter_dir / "agreement.log").read_text(encoding="utf-8").strip():
                    return True
            except OSError:
                pass

        if self.count_commits(iter_dir) > 0:
            return True

        return False


    def count_commits(self, iter_dir):
        total = 0
        for path in iter_dir.glob("client_stdout_*.log"):
            total += path.read_text(errors="ignore").count("committed tx=")
        return total


    def max_block_height(self, iter_dir):
        path = iter_dir / "blockCommits.csv"
        try:
            with path.open(newline="", encoding="utf-8") as f:
                rows = list(csv.DictReader(f))
        except FileNotFoundError:
            return 0.0

        heights = []
        for row in rows:
            try:
                heights.append(float(row["height"]))
            except (KeyError, TypeError, ValueError):
                continue
        return max(heights, default=0.0)


    def count_mutations(self, iter_dir):
        path = iter_dir / "mutations.csv"
        try:
            with path.open(newline="", encoding="utf-8") as f:
                return max(0, sum(1 for _ in csv.DictReader(f)))
        except FileNotFoundError:
            return 0


    def csv_has_rows(self, path):
        try:
            with path.open(newline="", encoding="utf-8") as f:
                return any(True for _ in csv.DictReader(f))
        except FileNotFoundError:
            return False


    def parse_run_output(self):
        totals = {
            "commits": 0,
            "agreement_violations": 0,
            "real_liveness": 0,
            "potential_liveness": 0,
            "max_height": 0.0,
            "mutations": 0,
        }

        for iter_dir in self.iter_dirs:
            ag, live, potential = self.count_agreement(iter_dir / "agreement.log")
            totals["commits"] += self.count_commits(iter_dir)
            totals["agreement_violations"] += ag
            totals["real_liveness"] = max(totals["real_liveness"], live)
            totals["potential_liveness"] += potential
            totals["max_height"] = max(totals["max_height"], self.max_block_height(iter_dir))
            totals["mutations"] += self.count_mutations(iter_dir)

        return totals


def main():
    parser = argparse.ArgumentParser(
        description="Evaluate fitness scores for one dsTest/evo output directory.",
    )
    parser.add_argument(
        "output_dir",
        type=Path,
        help="Directory containing exactly one populated iteration output.",
    )
    parser.add_argument(
        "--num-nodes",
        type=int,
        default=6,
        help="Number of Aptos validators expected in blockCommits.csv.",
    )
    args = parser.parse_args()

    config = {
        "dstest": {
            "num_nodes": args.num_nodes,
        },
    }
    evaluation = TestFitness(args.output_dir, config).evaluate()
    print(json.dumps(evaluation, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
