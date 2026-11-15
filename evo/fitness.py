import csv
import argparse
import json
import re
from pathlib import Path
from collections import defaultdict

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

    # Read blockCommits.csv and return normalized commit rows sorted by log time.
    # Invalid rows are skipped so fitness functions can tolerate partial output files.
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


    # Read metrics.csv and return normalized Prometheus samples sorted by log time.
    # labels_json is decoded once here so fitness functions can work with labels as a dict.
    def read_metrics(self, metrics_path):
        try:
            with Path(metrics_path).open(newline="", encoding="utf-8") as f:
                raw_rows = list(csv.DictReader(f))
        except FileNotFoundError:
            return []

        rows = []
        for row in raw_rows:
            try:
                rows.append({
                    "node_id": int(row["node_id"]),
                    "log_time": int(row["log_time"]),
                    "metric": row["metric"],
                    "labels": json.loads(row.get("labels_json") or "{}"),
                    "value": float(row["value"]),
                })
            except (KeyError, TypeError, ValueError, json.JSONDecodeError):
                continue

        return sorted(rows, key=lambda row: row["log_time"])


    # Return True if a metrics row has all labels requested by label_filter.
    # If no filter is given, every row matches.
    def labels_match(self, row, label_filter=None):
        if not label_filter:
            return True
        return all(row["labels"].get(k) == v for k, v in label_filter.items())


    # Compute the total increase of a cumulative Prometheus counter.
    # For each independent series (node_id, metric, labels), use last_value - first_value,
    # then sum across all series. This avoids overcounting repeated polling samples.
    def counter_delta(self, rows, metric, label_filter=None):
        series = defaultdict(list)

        for row in rows:
            if row["metric"] != metric or not self.labels_match(row, label_filter):
                continue

            label_key = tuple(sorted(row["labels"].items()))
            key = (row["node_id"], metric, label_key)
            series[key].append((row["log_time"], row["value"]))

        total = 0.0
        for values in series.values():
            values.sort()
            total += max(0.0, values[-1][1] - values[0][1])

        return total

    # Return the maximum observed value for a gauge-like metric.
    # Used for metrics where the current/high-watermark state matters more than a counter delta.
    def max_metric_value(self, rows, metric, label_filter=None):
        values = [
            row["value"]
            for row in rows
            if row["metric"] == metric and self.labels_match(row, label_filter)
        ]
        return max(values, default=0.0)

    # Compute delta statistics for a Prometheus histogram using its _sum and _count series.
    # Returns (total_observed_sum, total_observed_count, mean_value).
    def histogram_delta(self, rows, base_metric, label_filter=None):
        total = self.counter_delta(rows, f"{base_metric}_sum", label_filter)
        count = self.counter_delta(rows, f"{base_metric}_count", label_filter)
        mean = total / count if count > 0 else 0.0
        return total, count, mean
    

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


"""
This fitness function rewards executions in which more round timeout messages are required to commit a new block.
score = local_timeouts + timeout_rounds
"""
class RoundTimeoutCountFitness(FitnessFunction):
    name = "round_timeout_count_fitness"
    
    def evaluate(self, metrics_path, config) -> float:
        metrics = self.collect_metrics(metrics_path, config)
        return metrics["round_timeout_count_fitness"]
    
    # Reward executions that trigger more Aptos round-timeout behavior.
    # Score = local timeout counter delta + timeout-round counter delta.
    def collect_metrics(self, metrics_path, config):
        rows = self.read_metrics(metrics_path)

        local_timeouts = self.counter_delta(rows, "aptos_consensus_timeout_count")
        timeout_rounds = self.counter_delta(rows, "aptos_consensus_timeout_rounds_count")

        return {
            "round_timeout_count_fitness": local_timeouts + timeout_rounds,
            "local_timeout_count_delta": local_timeouts,
            "timeout_rounds_count_delta": timeout_rounds,
            "aggregated_round_timeout_reason_delta": self.counter_delta(
                rows, "aptos_consensus_agg_round_timeout_reason"
            ),
            "max_pending_round_timeouts": self.max_metric_value(
                rows, "aptos_consensus_pending_round_timeouts"
            ),
            "max_timeout_voted_power": self.max_metric_value(
                rows, "aptos_consensus_current_round_timeout_voted_power"
            ),
        }


"""
This fitness function rewards executions in which votes are split across different blocks.
5/5 votes for a block hash receives 0, 3/2 votes for a block receives 2 conflicting voting power (higher conflicts, higher reward).
"""
class VoteFragmentationFitness(FitnessFunction):
    name = "vote_fragmentation_fitness"
    
    def evaluate(self, metrics_path, config) -> float:
        metrics = self.collect_metrics(metrics_path, config)
        return metrics["vote_fragmentation_fitness"]
    
    # Reward executions where votes are split across different block/hash indexes.
    # Combines proposer-observed conflicting voting power with the largest live vote split snapshot.
    def collect_metrics(self, metrics_path, config):
        rows = self.read_metrics(metrics_path)

        snapshots = defaultdict(lambda: defaultdict(float))
        for row in rows:
            if row["metric"] != "aptos_consensus_current_round_voted_power":
                continue
            hash_index = row["labels"].get("hash_index")
            if hash_index is None:
                continue
            snapshots[(row["node_id"], row["log_time"])][hash_index] += row["value"]

        max_conflicting_power = 0.0
        max_fragmentation_ratio = 0.0
        max_distinct_hashes = 0

        for by_hash in snapshots.values():
            total = sum(by_hash.values())
            if total <= 0:
                continue
            majority = max(by_hash.values())
            conflicting = total - majority
            max_conflicting_power = max(max_conflicting_power, conflicting)
            max_fragmentation_ratio = max(max_fragmentation_ratio, conflicting / total)
            max_distinct_hashes = max(max_distinct_hashes, len(by_hash))

        proposer_conflicting = self.counter_delta(
            rows, "aptos_proposer_collected_conflicting_voting_power_sum"
        )

        return {
            "vote_fragmentation_fitness": proposer_conflicting + max_conflicting_power,
            "proposer_conflicting_voting_power_delta": proposer_conflicting,
            "proposer_most_voting_power_delta": self.counter_delta(
                rows, "aptos_proposer_collected_most_voting_power_sum"
            ),
            "proposer_timeout_voting_power_delta": self.counter_delta(
                rows, "aptos_proposer_collected_timeout_voting_power_sum"
            ),
            "proposer_collecting_round_count_delta": self.counter_delta(
                rows, "aptos_proposer_collecting_round_count"
            ),
            "max_current_round_conflicting_voting_power": max_conflicting_power,
            "max_vote_fragmentation_ratio": max_fragmentation_ratio,
            "max_distinct_vote_hashes": max_distinct_hashes,
        }
        

"""
This fitness function rewards executions that maximize mismatch between available batches 
and ordered blocks / delay between dissemination and commit.
"""
class QuorumStoreFitness(FitnessFunction):
    name = "quorum_store_fitness"
    
    def evaluate(self, metrics_path, config) -> float:
        metrics = self.collect_metrics(metrics_path, config)
        return metrics["quorum_store_fitness"]
    
    # Reward executions where QuorumStore data is delayed, missing, backlogged, or not ordered.
    # Combines missing payload/batch counts, available-vs-ordered mismatch, QS delays,
    # backlog averages, and expired/timed-out QS batches or proofs.
    def collect_metrics(self, metrics_path, config):
        rows = self.read_metrics(metrics_path)

        missing_payloads = self.counter_delta(
            rows, "aptos_consensus_proposal_payload_availability_count",
            {"status": "missing"},
        )
        available_batches = self.counter_delta(
            rows, "aptos_consensus_proposal_payload_batch_availability",
            {"state": "available"},
        )
        missing_batches = self.counter_delta(
            rows, "aptos_consensus_proposal_payload_batch_availability",
            {"state": "missing"},
        )

        ordered_batches = self.counter_delta(rows, "quorum_store_batch_num_per_block_sum")
        batch_mismatch = max(0.0, available_batches - ordered_batches) + missing_batches

        fetch_total, fetch_count, fetch_mean = self.histogram_delta(
            rows, "aptos_consensus_proposal_payload_fetch_duration"
        )
        pos_pull_total, pos_pull_count, pos_pull_mean = self.histogram_delta(
            rows, "quorum_store_pos_to_pull"
        )
        pos_commit_total, pos_commit_count, pos_commit_mean = self.histogram_delta(
            rows, "quorum_store_pos_to_commit"
        )

        txns_left_total, txns_left_count, avg_txns_left = self.histogram_delta(
            rows, "quorum_store_num_total_txns_left_on_update"
        )
        proofs_left_total, proofs_left_count, avg_proofs_left = self.histogram_delta(
            rows, "quorum_store_num_total_proofs_left_on_update"
        )
        txns_after_pull_total, _, avg_txns_after_pull = self.histogram_delta(
            rows, "quorum_store_num_txns_left_in_proof_queue_after_pull"
        )
        proofs_after_pull_total, _, avg_proofs_after_pull = self.histogram_delta(
            rows, "quorum_store_num_proofs_left_in_proof_queue_after_pull"
        )

        expired_or_timed_out = (
            self.counter_delta(rows, "quorum_store_batch_in_progress_expired")
            + self.counter_delta(rows, "quorum_store_batch_in_progress_timeout")
            + self.counter_delta(rows, "quorum_store_num_proofs_expired_when_commit")
        )

        backlog = (
            avg_txns_left
            + avg_proofs_left
            + avg_txns_after_pull
            + avg_proofs_after_pull
        )
        delay = fetch_total + pos_pull_total + pos_commit_total

        return {
            "quorum_store_fitness": (
                missing_payloads + batch_mismatch + delay + backlog + expired_or_timed_out
            ),
            "qs_missing_payloads_delta": missing_payloads,
            "qs_available_batches_delta": available_batches,
            "qs_missing_batches_delta": missing_batches,
            "qs_ordered_batches_delta": ordered_batches,
            "qs_batch_mismatch": batch_mismatch,
            "qs_payload_fetch_total_sec": fetch_total,
            "qs_payload_fetch_mean_sec": fetch_mean,
            "qs_pos_to_pull_total_sec": pos_pull_total,
            "qs_pos_to_pull_mean_sec": pos_pull_mean,
            "qs_pos_to_commit_total_sec": pos_commit_total,
            "qs_pos_to_commit_mean_sec": pos_commit_mean,
            "qs_avg_txns_left_on_update": avg_txns_left,
            "qs_avg_proofs_left_on_update": avg_proofs_left,
            "qs_avg_txns_left_after_pull": avg_txns_after_pull,
            "qs_avg_proofs_left_after_pull": avg_proofs_after_pull,
            "qs_expired_or_timed_out_delta": expired_or_timed_out,
        }
    

class TestFitness:
    def __init__(self, output_dir=None, config=None):
        self.fitnesses = [
            TimeFitness(),
            RoundStressFitness(),
            BlockHeightSkewFitness(),
            RoundTimeoutCountFitness(),
            VoteFragmentationFitness(),
            QuorumStoreFitness(),
        ]
        self.output_dir = Path(output_dir) if output_dir is not None else None
        self.config = config or {}
        self.iter_dir = None
    
    # Find the concrete iteration directory to evaluate.
    # Accept either a direct iteration directory or a parent directory containing *_byzzfuzz_<iter> dirs.
    def collect_iter_dir(self):
        if self.output_dir is None or not self.output_dir.exists():
            self.iter_dir = None
            return None

        parser = ViolationParser([])

        # Case 1: output_dir already is one concrete iteration directory.
        if parser.is_populated_iter_dir(self.output_dir):
            self.iter_dir = self.output_dir
            return self.iter_dir

        # Case 2: output_dir contains iteration directories, e.g. *_byzzfuzz_0.
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

    # Evaluate every available fitness for one execution and return:
    # - fitness_values: compact scores used by evo/plotting
    # - metrics: detailed diagnostic values
    # - buggy: whether agreement or real liveness violations were found
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
                    "round_timeout_count_fitness": -1000.0,
                    "vote_fragmentation_fitness": -1000.0,
                    "quorum_store_fitness": -1000.0,
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

                    "round_timeout_count_fitness": 0.0,
                    "local_timeout_count_delta": 0.0,
                    "timeout_rounds_count_delta": 0.0,
                    "aggregated_round_timeout_reason_delta": 0.0,
                    "max_pending_round_timeouts": 0.0,
                    "max_timeout_voted_power": 0.0,

                    "vote_fragmentation_fitness": 0.0,
                    "proposer_conflicting_voting_power_delta": 0.0,
                    "proposer_most_voting_power_delta": 0.0,
                    "proposer_timeout_voting_power_delta": 0.0,
                    "proposer_collecting_round_count_delta": 0.0,
                    "max_current_round_conflicting_voting_power": 0.0,
                    "max_vote_fragmentation_ratio": 0.0,
                    "max_distinct_vote_hashes": 0,

                    "quorum_store_fitness": 0.0,
                    "qs_missing_payloads_delta": 0.0,
                    "qs_available_batches_delta": 0.0,
                    "qs_missing_batches_delta": 0.0,
                    "qs_ordered_batches_delta": 0.0,
                    "qs_batch_mismatch": 0.0,
                    "qs_payload_fetch_total_sec": 0.0,
                    "qs_payload_fetch_mean_sec": 0.0,
                    "qs_pos_to_pull_total_sec": 0.0,
                    "qs_pos_to_pull_mean_sec": 0.0,
                    "qs_pos_to_commit_total_sec": 0.0,
                    "qs_pos_to_commit_mean_sec": 0.0,
                    "qs_avg_txns_left_on_update": 0.0,
                    "qs_avg_proofs_left_on_update": 0.0,
                    "qs_avg_txns_left_after_pull": 0.0,
                    "qs_avg_proofs_after_pull": 0.0,
                    "qs_expired_or_timed_out_delta": 0.0,
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
        
        round_timeout = RoundTimeoutCountFitness()
        round_timeout_metrics = round_timeout.collect_metrics(
            iter_dir / "metrics.csv", 
            self.config,
        )
        metrics.update(round_timeout_metrics)

        vote_fragmentation = VoteFragmentationFitness()
        vote_fragmentation_metrics = vote_fragmentation.collect_metrics(
            iter_dir / "metrics.csv", 
            self.config,
        )
        metrics.update(vote_fragmentation_metrics)

        quorum_store = QuorumStoreFitness()
        quorum_store_metrics = quorum_store.collect_metrics(
            iter_dir / "metrics.csv", 
            self.config,
        )
        metrics.update(quorum_store_metrics)

        has_violation = (
            metrics["agreement_violations"] > 0
            or metrics["real_liveness"] > 0
        )
 
        return {
            "fitness_values": {
                "time_fitness": time_metrics["max_inter_block_latency_sec"],
                "round_stress_fitness": round_stress_metrics["max_round_gap_between_committed_blocks"],
                "block_height_skew_fitness": height_skew_metrics["max_block_height_skew"],
                "round_timeout_count_fitness": round_timeout_metrics["round_timeout_count_fitness"],
                "vote_fragmentation_fitness": vote_fragmentation_metrics["vote_fragmentation_fitness"],
                "quorum_store_fitness": quorum_store_metrics["quorum_store_fitness"],
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
    
    # Count semantic violations from agreement.log.
    # Agreement is counted once per disagreed height; real liveness is boolean per iteration;
    # potential liveness counts every logged potential timeout.
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

    # Return True if an iteration directory contains meaningful output.
    # A real iteration should at least have block commit rows / metrics rows.
    # This skips leftover dirs that only contain an empty/header-only mutations.csv.
    def is_populated_iter_dir(self, iter_dir):
        if self.csv_has_rows(iter_dir / "blockCommits.csv"):
            return True
        
        if self.csv_has_rows(iter_dir / "metrics.csv"):
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

    # Count successfully committed client transactions from client stdout logs.
    def count_commits(self, iter_dir):
        total = 0
        for path in iter_dir.glob("client_stdout_*.log"):
            total += path.read_text(errors="ignore").count("committed tx=")
        return total

    # Return the maximum block height observed in blockCommits.csv.
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

    # Count applied mutations from mutations.csv using CSV parsing because message fields may contain newlines.
    def count_mutations(self, iter_dir):
        path = iter_dir / "mutations.csv"
        try:
            with path.open(newline="", encoding="utf-8") as f:
                return max(0, sum(1 for _ in csv.DictReader(f)))
        except FileNotFoundError:
            return 0

    # Return True if a CSV exists and has at least one data row after the header.
    def csv_has_rows(self, path):
        try:
            with path.open(newline="", encoding="utf-8") as f:
                return any(True for _ in csv.DictReader(f))
        except FileNotFoundError:
            return False

    # Aggregate basic execution outputs across one or more iteration directories.
    # Used to classify buggy vs non-buggy runs and attach common diagnostics.
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
