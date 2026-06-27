from dataclasses import dataclass
import csv
import json
import re
import subprocess
import time
import shutil
from pathlib import Path

THIS_FILE = Path(__file__).resolve()
EVO_DIR = THIS_FILE.parent
DSTEST_ROOT = EVO_DIR.parent

ITER_DIR_RE = re.compile(r"_(\d+)$")

BENCHMARK_BUG_FLAGS = {
    "aptos": {"BUG1": "false", "BUG2": "false", "BUG3": "false"},
    "bug1": {"BUG1": "true", "BUG2": "false", "BUG3": "false"},
    "bug2": {"BUG1": "false", "BUG2": "true", "BUG3": "false"},
    "bug3": {"BUG1": "false", "BUG2": "false", "BUG3": "true"},
}

# ************************************************* #
# 				 Helpers				            #
# ************************************************* #

# For evo individuals, return the path to the fault plan utilized.
# For randomized Byzzfuzz, we return an empty string (no path).
def write_fault_plan(individual, log_dir):
    if individual is None:
        return ""

    path = log_dir / "individual_fault_plan.yaml"
    path.write_text(individual.to_yaml(), encoding="utf-8")
    return str(path.resolve())


def copy_evo_experiment_config(log_dir):
    src = EVO_DIR / "configs" / "aptos_evo.yaml"
    dst = log_dir / "evo_experiment_config.yaml"

    if src.exists():
        shutil.copy2(src, dst)

    return str(dst.resolve())

# ************************************************* #
# 				 Make result and task				#
# ************************************************* #

@dataclass
class MakeResult:
    target: str
    returncode: int
    elapsed: float
    output: str


class MakeTask:
    def __init__(self, config, timeout_sec: int, result_log: Path):
        self.config = config
        self.vars   = {}
        self.timeout_sec = timeout_sec
        self.result_log = Path(result_log)
    
    
    @staticmethod
    def dstest_param(config, key, default=None):
        dstest = config.get("dstest", {}) or {}
        return dstest.get(key, config.get(key, default))


    @staticmethod
    def safe_name(value):
        return re.sub(r"[^A-Za-z0-9_.-]+", "-", str(value)).strip("-")

    
    def prepare(self, individual, log_dir, fault_plan_path):
        benchmark = self.config.get("benchmark", "aptos")
        if benchmark not in BENCHMARK_BUG_FLAGS:
            raise ValueError(f"unknown benchmark: {benchmark}")
        
        bug_flags = BENCHMARK_BUG_FLAGS[benchmark]

        run_id = self.safe_name(f"{self.config['config_combo_id']}-{log_dir.name}")
        run_tag = str(self.config.get("run_tag", "-evo-s0"))

        if individual is None:
            c = int(self.dstest_param(self.config, "c", 0))
            d = int(self.dstest_param(self.config, "d", 0))
            r = int(self.dstest_param(self.config, "r", 1))
        else:
            plan = individual.to_plan_dict()
            c = plan["c"]
            d = plan["d"]
            r = plan["r"]
        
        self.vars = {
            "RUN_ID": run_id,
            "RUN_TAG": run_tag,
            "RUN_OFFSET": int(self.config.get("run_offset", 0)),
            "TEST_NAME": self.safe_name(f"{benchmark}-{self.config['strategy']}"),
            "OUTPUT_BASE": str((log_dir / "dstest_output").resolve().relative_to(DSTEST_ROOT)),
            "CONFIG": str((log_dir / "aptos.yml").resolve()),
            "DSTEST_LOG": str((log_dir / "dstest.log").resolve()),
            "EVOFAULTPLAN": fault_plan_path,
            "PARAM_C": c,
            "PARAM_D": d,
            "PARAM_R": r,
            "STEPS": int(self.dstest_param(self.config, "steps", 2300)),
            "BLOCKBUDGET": int(self.dstest_param(self.config, "block_height_max", 10)),
            "RECOVERYSECONDS": int(self.dstest_param(self.config, "recovery_seconds", 30)),
            "LIVENESSTIMEOUT": int(self.dstest_param(self.config, "liveness_timeout", 60)),
            "SEED": int(self.config.get("seed", 42)),
            "NUM_REPLICAS": int(self.dstest_param(self.config, "num_nodes", 6)),
            "NUM_CLIENT_ACCOUNTS": int(self.dstest_param(self.config, "num_client_accounts", 2)),
            "ITERATIONS": int(self.dstest_param(self.config, "iterations", 1)),
            **bug_flags,
        }
    
  
    # Run the given make task with target. It writes the stdout captured into result_log. 
    # If the cmd hangs longer than timeout_sec, we return code 124.
    def run(self, target):
        cmd = ["make", "-C", str(DSTEST_ROOT)]
        cmd.extend(f"{k}={v}" for k, v in self.vars.items())
        cmd.append(target)

        started = time.time()
        try:
            proc = subprocess.run(
                cmd,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=self.timeout_sec,
            )
            output = proc.stdout or ""
            returncode = proc.returncode
        
        except subprocess.TimeoutExpired as err:
            output = err.stdout or err.output or ""
            if isinstance(output, bytes):
                output = output.decode(errors="replace")
            returncode = 124

        elapsed = time.time() - started
        
        with self.result_log.open("a", encoding="utf-8") as f:
            f.write(f"\n\n===== {' '.join(cmd)} =====\n")
            f.write(output)
            f.write(f"\n[returncode={returncode} elapsed={elapsed:.3f}s]\n")

        return MakeResult(
            target=target,
            returncode=returncode,
            elapsed=elapsed,
            output=output,
        )


    def slot_ready(self):
        base_dir = Path(f"/tmp/aptos-dstest{self.vars['RUN_TAG']}")
        num_replicas = int(self.vars["NUM_REPLICAS"])
        num_client_accounts = int(self.vars.get("NUM_CLIENT_ACCOUNTS", 2))

        if not (base_dir / "genesis/genesis.blob").exists():
            return False

        if not (base_dir / "genesis/waypoint.txt").exists():
            return False

        for i in range(num_replicas):
            node_dir = base_dir / "nodes" / f"v{i}"
            if not (node_dir / "node.yaml").exists():
                return False
            if not (node_dir / "genesis/validator-identity.yaml").exists():
                return False

        for i in range(num_client_accounts):
            if not (base_dir / "genesis" / "clients" / f"c{i:02d}.yaml").exists():
                return False

        return True


    def ensure_slot_ready(self):
        if self.slot_ready():
            return None

        # seeds implicitly runs node-configs
        result = self.run("seeds")
        if result.returncode != 0:
            return result

        return None


# ************************************************* #
# 				 Parsers for output				    #
# ************************************************* #
# For one output run

def count_agreement(path):
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


def count_commits(iter_dir):
    total = 0
    for path in iter_dir.glob("client_stdout_*.log"):
        total += path.read_text(errors="ignore").count("committed tx=")
    return total


def max_block_height(iter_dir):
    path = iter_dir / "blockCommits.csv"
    try:
        with path.open(newline="", encoding="utf-8") as f:
            rows = list(csv.DictReader(f))
    except FileNotFoundError:
        return 0.0

    heights = [float(row["height"]) for row in rows if row.get("height")]
    return max(heights) if heights else 0.0


def count_mutations(iter_dir):
    path = iter_dir / "mutations.csv"
    try:
        with path.open(newline="", encoding="utf-8") as f:
            return max(0, sum(1 for _ in csv.DictReader(f)))
    except FileNotFoundError:
        return 0


def csv_has_rows(path):
    try:
        with path.open(newline="", encoding="utf-8") as f:
            return any(True for _ in csv.DictReader(f))
    except FileNotFoundError:
        return False


def is_populated_iter_dir(iter_dir):
    # A real iteration should at least have block commit rows.
    # This skips leftover dirs that only contain an empty/header-only mutations.csv.
    if csv_has_rows(iter_dir / "blockCommits.csv"):
        return True

    if (iter_dir / "agreement.log").exists():
        try:
            if (iter_dir / "agreement.log").read_text(encoding="utf-8").strip():
                return True
        except OSError:
            pass

    if count_commits(iter_dir) > 0:
        return True

    return False


def parse_run_output(output_dir):
    iter_dirs = [
        p for p in output_dir.iterdir()
        if p.is_dir()
        and ITER_DIR_RE.search(p.name)
        and is_populated_iter_dir(p)
    ] if output_dir.exists() else []
    
    totals = {
        "iterations_seen": len(iter_dirs),
        "commits": 0,
        "agreement_violations": 0,
        "real_liveness": 0,
        "potential_liveness": 0,
        "max_height": 0.0,
        "mutations": 0,
    }

    for iter_dir in iter_dirs:
        ag, live, potential = count_agreement(iter_dir / "agreement.log")
        totals["commits"] += count_commits(iter_dir)
        totals["agreement_violations"] += ag
        totals["real_liveness"] = max(totals["real_liveness"], live)
        totals["potential_liveness"] += potential
        totals["max_height"] = max(totals["max_height"], max_block_height(iter_dir))
        totals["mutations"] += count_mutations(iter_dir)

    return totals


# ************************************************* #
# 				 Fitness scores				        #
# ************************************************* #


def compute_fitness(metrics, duration_sec, config):
    if metrics["iterations_seen"] == 0:
        return -1000.0

    name = config.get("fitness_name", "execution_time")

    if name == "execution_time":
        return (
            duration_sec
            + 2000.0 * metrics["agreement_violations"]
            + 1000.0 * metrics["real_liveness"]
            + 100.0 * metrics["potential_liveness"]
        )

    if name == "qc":
        raise NotImplementedError("qc fitness is not implemented yet")

    raise ValueError(f"unknown fitness function: {name}")


# ************************************************* #
# 				 Run DSTest and evaluate	        #
# ************************************************* #


def failure_result(log_dir, fault_plan_path, output_dir, duration_sec, phase, returncode):
    result = {
        "fitness": -1000.0,
        "violation": 0,
        "agreement": False,
        "liveness": False,
        "duration_sec": duration_sec,
        "returncode": returncode,
        "failed_phase": phase,
        "infrastructure_error": True,
        "output_dir": str(output_dir),
        "fault_plan": fault_plan_path,
        "iterations_seen": 0,
        "commits": 0,
        "agreement_violations": 0,
        "real_liveness": 0,
        "potential_liveness": 0,
        "max_height": 0.0,
        "mutations": 0,
    }
    (log_dir / "result.json").write_text(json.dumps(result, indent=2, sort_keys=True), encoding="utf-8")
    return result


# Receives one AptosEncoding individual, creates a unique eval dir that contains the individual's fault plan.
# We should call: " make node-configs seeds clean config run " in this order. 
# For each run, this does: setup the localnet, clean, make the config, run and capture the ouput.
#
# Each individual directory has: 
#   - evo_experiment_config.yaml      # global evolutionary config
#   - individual_fault_plan.yaml      # this individual’s generated plan
#   - aptos.yml                       # dstest config generated by Makefile
#
#
def run_dstest_and_evaluate(individual, config, log_dir):
    log_dir = Path(log_dir)
    log_dir.mkdir(parents=True, exist_ok=True)

    make_log = log_dir / "make.log"
    fault_plan_path = write_fault_plan(individual, log_dir)
    evo_config_path = copy_evo_experiment_config(log_dir)
    timeout_sec = int(config.get("subprocess_timeout_sec", 900))

    make_task = MakeTask(config, timeout_sec, make_log)
    make_task.prepare(individual, log_dir, fault_plan_path)

    output_dir = DSTEST_ROOT / make_task.vars["OUTPUT_BASE"] / make_task.vars["RUN_ID"]
    
    start = time.time()
    
    failed = make_task.ensure_slot_ready()
    if failed is not None:
        return failure_result(
            log_dir,
            fault_plan_path,
            output_dir,
            time.time() - start,
            failed.target,
            failed.returncode,
        )

    for target in ("clean", "config"):
        result = make_task.run(target)
        if result.returncode != 0:
            return failure_result(
                log_dir,
                fault_plan_path,
                output_dir,
                time.time() - start,
                result.target,
                result.returncode,
            )

    # Blocking, worker waits until make run finishes or hits timeout_sec
    run_result = make_task.run("run")

    duration_sec = time.time() - start
    metrics = parse_run_output(output_dir)
    
    has_semantic_violation = (
        metrics["agreement_violations"] > 0
        or metrics["real_liveness"] > 0
    )

    infrastructure_error = (
        metrics["iterations_seen"] == 0
        or (run_result.returncode != 0 and not has_semantic_violation)
    )

    fitness = (
        -1000.0
        if infrastructure_error
        else compute_fitness(metrics, duration_sec, config)
    )

    result = {
        "fitness": fitness,
        "violation": int(has_semantic_violation),
        "agreement": bool(metrics["agreement_violations"]),
        "liveness": bool(metrics["real_liveness"]),
        "duration_sec": duration_sec,
        "returncode": run_result.returncode,
        "run_returncode": run_result.returncode,
        "infrastructure_error": infrastructure_error,
        "output_dir": str(output_dir),
        "fault_plan": fault_plan_path,
        "evo_experiment_config": evo_config_path,
        **metrics,
    }
    
    (log_dir / "result.json").write_text(
        json.dumps(result, indent=2, sort_keys=True),
        encoding="utf-8",
    )

    return result
