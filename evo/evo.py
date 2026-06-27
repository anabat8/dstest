import copy
import csv
import threading
import random
from concurrent.futures import ProcessPoolExecutor
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

import yaml
from deap import algorithms, base, creator, tools

import encoding
from run import run_dstest_and_evaluate


THIS_FILE = Path(__file__).resolve()
CUR_DIR = THIS_FILE.parent
CONFIG_FILE = CUR_DIR / "configs/aptos_evo.yaml"
LOGS_DIR = CUR_DIR / "logs"
STRATEGY_TO_ENCODING = {
    "evo_aptos": "AptosEncoding",
}


@dataclass(frozen=True)
class EvaluationTask:
    config_combo_id: str    # for each thread
    generation: int         # for byzzfuzz(random), it's just the batch number
    individual_id: int      # in each generation
    individual: Any
    config: dict[str, Any]  # other config stuff


# ************************************************* #
# 	        Loading plan config				        #
# ************************************************* #


def load_config():
    with open(CONFIG_FILE, "r", encoding="utf-8") as f:
        return yaml.safe_load(f) or {}


# Iterates through the config file and builds all combinations of run configs between 
# all strategies, benchmarks and fitnesses defined.
# Each run config has one:
#   - strategy
#   - benchmark
#   - fitness_name
#   - log_root_dir
#   - config_combo_id
# Each run config will correspond to one thread. All threads will share the same process pool.
def build_run_configs(config, run_timestamp):
    run_configs = []
    config_combo_id = 0

    for strategy in config["strategies"]:
        for benchmark in config["benchmarks"]:
            for fitness_name in config["fitness"]:
                run_config = copy.deepcopy(config)
                run_config["strategy"] = strategy
                run_config["benchmark"] = benchmark
                run_config["fitness_name"] = fitness_name
                run_config["log_root_dir"] = str(
                    LOGS_DIR / run_timestamp / benchmark / strategy / fitness_name
                )
                run_config["config_combo_id"] = (
                    f"E{config_combo_id:03d}_{strategy}_{benchmark}_{fitness_name}"
                )
                run_configs.append(run_config)
                config_combo_id += 1

    return run_configs


# ************************************************* #
# 	        Building encoding config				#
# ************************************************* #

# Gets the encoding class based on the strategy given in the config, by looking it up
# in the STRATEGY_TO_ENCODING dict
def get_encoding_cls(config):
    strategy = config["strategy"]
    encoding_name = STRATEGY_TO_ENCODING.get(strategy)
    if encoding_name is None:
        raise ValueError(f"No encoding class configured for strategy: {strategy}")
    encoding_cls = getattr(encoding, encoding_name, None)
    if encoding_cls is None:
        raise ValueError(f"Unknown encoding class: {encoding_name}")
    if not issubclass(encoding_cls, encoding.BaseEncoding):
        raise ValueError(f"{encoding_name} is not a BaseEncoding subclass")
    return encoding_cls


# setdefault is just a short hand for get(key) if key is not int the dict (insert and return the default)
def make_encoding_config(config):
    encoding_config = copy.deepcopy(config)
    encoding_config.update(copy.deepcopy(config.get("dstest", {}) or {}))
    return encoding_config


# ************************************************* #
# 	        Helpers 				                #
# ************************************************* #


def sample_individual(config, encoding_cls):
    ind = encoding_cls.sample(make_encoding_config(config))
    ind.fitness = creator.FitnessMax()
    return ind


# somehow deap's registery mechanism is global, so you cannot register the same type concurrently
def setup_deap_types():
    if not hasattr(creator, "FitnessMax"):
        creator.create("FitnessMax", base.Fitness, weights=(1.0,)) # single objective, weights should be a tuple


def evaluate_task_worker(task: EvaluationTask):
    log_dir = (
        Path(task.config["log_root_dir"]) / f"G{task.generation}T{task.individual_id}"
    )
    run_result = run_dstest_and_evaluate(
        task.individual,
        task.config,
        log_dir,
    )
    return {
        "config_combo_id": task.config_combo_id,
        "generation": task.generation,
        "individual_id": task.individual_id,
        **run_result,
    }


def evaluate_batch(pool, config, generation, individuals):
    futures = []
    for individual_id, ind in enumerate(individuals, start=1):
        task = EvaluationTask(
            config_combo_id=config["config_combo_id"],
            generation=generation,
            individual_id=individual_id,
            individual=ind,
            config=config,
        )
        future = pool.submit(evaluate_task_worker, task)
        futures.append((future, ind))

    results = []
    for future, ind in futures:
        result = future.result()
        results.append(result)
        if ind is not None:
            ind.fitness.values = (result["fitness"],)
            ind.eval_result = result
    return results


# just to demonstrate some deap's stats usage, not really necessary to the main loop
def init_fitness_csv(config):
    log_root_dir = Path(config["log_root_dir"])
    log_root_dir.mkdir(parents=True, exist_ok=True)
    fitness_csv = log_root_dir / "fitness.csv"
    with open(fitness_csv, "w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["mode", "index", "min_fitness", "avg_fitness", "max_fitness"])


def write_population_stats(config, generation, individuals):
    stats = tools.Statistics(lambda ind: ind.fitness.values[0])
    stats.register("min", min)
    stats.register("avg", lambda values: sum(values) / len(values))
    stats.register("max", max)
    record = stats.compile(individuals)
    fitness_csv = Path(config["log_root_dir"]) / "fitness.csv"
    with open(fitness_csv, "a", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["generation", generation, record["min"], record["avg"], record["max"]])


def write_batch_stats(config, batch_id, results):
    fitness_values = [result["fitness"] for result in results]
    avg_fitness = sum(fitness_values) / len(fitness_values)
    fitness_csv = Path(config["log_root_dir"]) / "fitness.csv"
    with open(fitness_csv, "a", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["batch", batch_id, min(fitness_values), avg_fitness, max(fitness_values)])


# ************************************************* #
# 	        Byzzfuzz(random) strategy				#
# ************************************************* #


def run_random_batches(config, pool):
    population_size = int(config["population_size"])
    total_num_tests = int(config["total_num_tests"])
    completed = 0
    batch_id = 0

    while completed < total_num_tests:
        batch_size = min(population_size, total_num_tests - completed)
        individuals = [None for _ in range(batch_size)]
        results = evaluate_batch(pool, config, batch_id, individuals)
        write_batch_stats(config, batch_id, results)
        completed += batch_size
        batch_id += 1


# ************************************************* #
# 	        Evolutionary strategy				    #
# ************************************************* #


def run_evolution(config, pool, encoding_cls):
    # encoding_cls: a subclass of BaseEncoding
    population_size = int(config["population_size"])
    mu = int(config["mu"])
    total_num_tests = int(config["total_num_tests"])
    max_generation = max(1, total_num_tests // population_size)

    toolbox = base.Toolbox()
    toolbox.register("individual", sample_individual, config, encoding_cls)
    toolbox.register("population", tools.initRepeat, list, toolbox.individual)
    toolbox.register("mate", encoding_cls.mate)
    toolbox.register("mutate", encoding_cls.mutate)
    toolbox.register("select", tools.selBest)

    population = toolbox.population(n=population_size)
    evaluate_batch(pool, config, 0, population)
    write_population_stats(config, 0, population)
    population = toolbox.select(population, mu)

    for generation in range(1, max_generation):
        offspring = algorithms.varOr(
            population,
            toolbox,
            lambda_=population_size,
            cxpb=0.7,
            mutpb=0.3,
        )
        invalid_ind = [ind for ind in offspring if not ind.fitness.valid]
        if invalid_ind:
            evaluate_batch(pool, config, generation, invalid_ind)
        combined_population = population + offspring
        write_population_stats(config, generation, combined_population)
        population = toolbox.select(combined_population, mu)


# ************************************************* #
# 	        Run threads				                #
# ************************************************* #

def runner_thread_main(config, pool):
    strategy = config["strategy"]
    init_fitness_csv(config)
    print(f"[{config['config_combo_id']}] start strategy={strategy}", flush=True)

    if strategy == "byzzfuzz":
        run_random_batches(config, pool)
    else:
        encoding_cls = get_encoding_cls(config)
        run_evolution(config, pool, encoding_cls)

    print(f"[{config['config_combo_id']}] done", flush=True)


# Structure: multiple threads, each thread in charge of running one configuration (benchmark+encoding+fitness+.. combo)
# Multiple threads share the same process pool
# Threads submit tasks to the pool in batches
# The pool is in charge of scheduling tasks
# Each task runs one individual and returns a fitness value
def main():
    base_config = load_config()
    random.seed(int(base_config.get("seed", 42)))
    run_timestamp = datetime.now().strftime("%Y_%m_%d_%Hh%Mm_%Ss")
    run_configs = build_run_configs(base_config, run_timestamp)
    setup_deap_types()

    max_workers = int(base_config.get("max_parallel_workers", 1))
    threads = []

    with ProcessPoolExecutor(max_workers=max_workers) as pool:
        for run_config in run_configs:
            thread = threading.Thread(
                target=runner_thread_main,
                args=(run_config, pool),
            )
            thread.start()
            threads.append(thread)

        for thread in threads:
            thread.join()


if __name__ == "__main__":
    main()