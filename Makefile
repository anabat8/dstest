# -----------------------------
# Config
# -----------------------------
DSTEST_ROOT := $(abspath .)
APTOS_DIR   := $(DSTEST_ROOT)/aptos

APTOS_CORE ?= $(abspath $(DSTEST_ROOT)/../aptos-core)

DSTEST_BIN  := $(DSTEST_ROOT)/cmd/dstest/main
APTOS_NODE  := $(APTOS_CORE)/target/release/aptos-node
APTOS_CLI   := $(APTOS_CORE)/target/cli/aptos
APTOS_CLIENT_BIN := $(DSTEST_ROOT)/cmd/aptos_client/main

# venv
VENV_DIR := $(DSTEST_ROOT)/.venv
VENV_PY  := $(VENV_DIR)/bin/python3
PIP      := $(VENV_DIR)/bin/pip

# Per-run tag (string suffix) and port offset, for running parallel experiments
RUN_TAG    ?=
RUN_OFFSET ?= 0

# base directory for the generated genesis + node configs
BASE_DIR ?= /tmp/aptos-dstest$(RUN_TAG)

# Log for DSTest output 
DSTEST_LOG ?= /tmp/dstest$(RUN_TAG).log

# dsTest config path
CONFIG ?= $(DSTEST_ROOT)/aptos/configs/aptos$(RUN_TAG).yml

# localnet config
NUM_REPLICAS ?= 4
CHAIN_ID ?= 42
EPOCH_DURATION_SECS ?= 7200

# Genesis artifacts
GENESIS_DIR  := $(BASE_DIR)/genesis
FRAMEWORK_MRB := $(GENESIS_DIR)/framework.mrb

# node config ports
BASE_PORT ?= $(shell expr 8000 + $(RUN_OFFSET))

# DSTest interceptor ports
BASE_INTERCEPTOR_PORT ?= $(shell expr 10000 + $(RUN_OFFSET))

# validator and fullnode network ports used in genesis for discovery
VAL_NET_BASE ?= $(shell expr 6100 + $(RUN_OFFSET))
FN_NET_BASE  ?= $(shell expr 6200 + $(RUN_OFFSET))

# number of client accounts to generate and pre-fund (reusable)
NUM_CLIENT_ACCOUNTS ?= 4

# logs output directory
RUN_ID ?= $(shell date +"%Y%m%d_%H%M%S")
OUTPUT_BASE ?= output/aptos$(RUN_TAG)
OUTPUT_DIR ?= $(OUTPUT_BASE)/$(RUN_ID)

# for filtering the logs output
FILTER_OUT ?= $(OUTPUT_DIR)/consensus_filtered.jsonl
FILTER_TEXT ?= $(OUTPUT_DIR)/consensus_filtered.log

# for generating a config
# TEST_NAME is an experiment's name, can be anything
# SCHED_TYPE can be byzzfuzz, random, pct, ql
TEST_NAME ?= aptos-localnet
SCHED_TYPE ?= byzzfuzz

# logging pass-through (empty by default)
RUST_LOG ?= byzzfuzz.noise=info,info
LOG_LEVEL ?=

# for checking liveness property, we set a timeout bound (in seconds)
LIVENESS_TIMEOUT ?= 30

# aptos.yml template vars
SEED                 ?= 42
PARAM_C              ?= 10
PARAM_D              ?= 0
PARAM_R              ?= 10
# Note: CLIENT_REQUESTS is not currently read by ByzzFuzz Scheduler.
CLIENT_REQUESTS      ?= 0

# -----------------------------
# Helpers
# -----------------------------
.PHONY: help
help:
	@echo "Targets:"
	@echo "  make setup                 # create venv + install python deps"
	@echo "  make build                 # build aptos-node + aptos-cli + dstest + aptos-client"
	@echo "  make framework    			# build framework.mrb (cached)"
	@echo "  make genesis               # generate genesis.blob + waypoint.txt"
	@echo "  make node-configs          # generate node.yaml + per-node dirs"
	@echo "  make seeds                 # patch seed_addrs"
	@echo "  make client-accounts       # generate workload accounts"
	@echo "  make config                # generate config file aptos.yml for dsTest"
	@echo "  make clean                 # kill nodes + wipe state"
	@echo "  make run                   # run dsTest with CONFIG"
	@echo "  make all                   # build + genesis + node-configs + seeds + config + run"
	@echo "  make filter-logs           # filter the consensus log output"
	@echo "  make run-and-filter        # run dsTest with CONFIG and filter the logs"
	@echo "  make plot                  # plot graphs for a run"
	@echo "  make plot-latest           # plot graphs for the latest run under OUTPUT_BASE"
	@echo "  make aggregate             # aggregate results across runs under OUTPUT_BASE"
	@echo ""
	@echo "Vars:"
	@echo "  APTOS_CORE=$(APTOS_CORE)"
	@echo "  BASE_DIR=$(BASE_DIR)"
	@echo "  NUM_REPLICAS=$(NUM_REPLICAS)"
	@echo "  BASE_PORT=$(BASE_PORT)"
	@echo "  CONFIG=$(CONFIG)"
	@echo "  RUST_LOG=$(RUST_LOG)"
	@echo "  LOG_LEVEL=$(LOG_LEVEL)"

# -----------------------------
# Python venv + deps
# -----------------------------
$(VENV_PY):
	python3 -m venv $(VENV_DIR)

.PHONY: setup
setup: $(VENV_PY)
	$(PIP) install --upgrade pip
	$(PIP) install pyyaml cryptography matplotlib pandas

# -----------------------------
# Builds
# -----------------------------
.PHONY: build-aptos
build-aptos:
	cd $(APTOS_CORE) && cargo build --release -p aptos-node --features byzzfuzz
	cd $(APTOS_CORE) && cargo build -p aptos --profile cli

.PHONY: build-dstest
build-dstest:
	cd $(DSTEST_ROOT)/cmd/dstest && go build -o main .

.PHONY: build-aptos-client
build-aptos-client:
	cd $(DSTEST_ROOT)/cmd/aptos_client && go build -o main .

.PHONY: build
build: build-aptos build-dstest build-aptos-client
	@test -x $(APTOS_NODE)        || (echo "Missing $(APTOS_NODE) (build-aptos failed?)"; exit 1)
	@test -x $(APTOS_CLI)         || (echo "Missing $(APTOS_CLI)  (build-aptos failed?)"; exit 1)
	@test -x $(DSTEST_BIN)        || (echo "Missing $(DSTEST_BIN) (build-dstest failed?)"; exit 1)
	@test -x $(APTOS_CLIENT_BIN)  || (echo "Missing $(APTOS_CLIENT_BIN) (build-aptos-client failed?)"; exit 1)

# -----------------------------
# Aptos Framework MRB
# -----------------------------
# Built once per aptos-core checkout at a shared cache location. Each run's
# per-BASE_DIR FRAMEWORK_MRB is just a copy of this artifact.
APTOS_HEAD_MRB := $(APTOS_CORE)/head.mrb

$(APTOS_HEAD_MRB):
	@echo "[framework] Building $@ ..."
	cd "$(APTOS_CORE)" && cargo run -p aptos-framework -- release --target head >/dev/null
	@test -f "$@" || (echo "ERROR: $@ missing after cargo build"; exit 1)

$(FRAMEWORK_MRB): $(APTOS_HEAD_MRB)
	@mkdir -p "$(GENESIS_DIR)"
	@cp -f "$<" "$@"
	@echo "[framework] $@ (from $<)"

.PHONY: framework
framework: $(FRAMEWORK_MRB)

# -----------------------------
# Genesis + node configs
# -----------------------------
.PHONY: genesis
genesis: framework setup build
	@mkdir -p $(BASE_DIR)
	BASE_DIR="$(BASE_DIR)" \
	GENESIS_DIR="$(GENESIS_DIR)" \
	FRAMEWORK_MRB="$(FRAMEWORK_MRB)" \
	NUM_NODES="$(NUM_REPLICAS)" \
	CHAIN_ID="$(CHAIN_ID)" \
	EPOCH_DURATION_SECS="$(EPOCH_DURATION_SECS)" \
	APTOS_CORE="$(APTOS_CORE)" \
	APTOS_CLI="$(APTOS_CLI)" \
	PYTHON_BIN="$(VENV_PY)" \
	VAL_NET_BASE="$(VAL_NET_BASE)" \
	FN_NET_BASE="$(FN_NET_BASE)" \
	bash $(APTOS_DIR)/aptos_prepare_localnet.sh

.PHONY: node-configs
node-configs: genesis client-accounts
	BASE_DIR=$(BASE_DIR) \
	GENESIS_DIR="$(GENESIS_DIR)" \
	APTOS_CORE="$(APTOS_CORE)" \
	NUM_NODES="$(NUM_REPLICAS)" \
	BASE_PORT="${BASE_PORT}" \
	PYTHON_BIN="$(VENV_PY)" \
	bash $(APTOS_DIR)/aptos_make_node_configs.sh

# Seed patch
.PHONY: seeds
seeds: node-configs config
	BASE_DIR="$(BASE_DIR)" CONFIG="$(CONFIG)" \
	"$(VENV_PY)" "$(APTOS_DIR)/aptos_update_seeds.py"

# -----------------------------------------------------------------
# Client accounts generation for submitting txs to the blockchain
# -----------------------------------------------------------------
.PHONY: client-accounts
client-accounts: genesis
	BASE_DIR="$(BASE_DIR)" \
	GENESIS_DIR="$(GENESIS_DIR)" \
	APTOS_CORE="$(APTOS_CORE)" \
	APTOS_CLI="$(APTOS_CLI)" \
	NUM_CLIENT_ACCOUNTS="$(NUM_CLIENT_ACCOUNTS)" \
	bash $(APTOS_DIR)/aptos_setup_client_accounts.sh

# -----------------------------
# dsTest config generation
# -----------------------------
.PHONY: config
config:
	@mkdir -p "$(OUTPUT_DIR)"
	@mkdir -p $(APTOS_DIR)/configs
	@echo "Writing $(CONFIG)"
	@{ \
	echo "TestConfig:"; \
	echo "  Name: $(TEST_NAME)"; \
	echo "  Experiments: 1"; \
	echo "  Iterations: 10"; \
	echo "  WaitDuration: 50"; \
	echo "  StartupDuration: 10"; \
	echo ""; \
	echo "SchedulerConfig:"; \
	echo "  Type: \"$(SCHED_TYPE)\""; \
	echo "  Steps: 1000"; \
	echo "  Seed: $(SEED)"; \
	echo "  ClientRequests: $(CLIENT_REQUESTS)"; \
	echo "  Params: {\"c\": $(PARAM_C), \"d\": $(PARAM_D), \"r\": $(PARAM_R)}"; \
	echo ""; \
	echo "NetworkConfig:"; \
	echo "  BaseReplicaPort: $(BASE_PORT)"; \
	echo "  BaseInterceptorPort: $(BASE_INTERCEPTOR_PORT)"; \
	echo "  Protocol: \"aptostcp\""; \
	echo "  MessageType: Aptos"; \
	echo ""; \
	echo "FaultConfig:"; \
	echo "  Faults: []"; \
	echo ""; \
	echo "ProcessConfig:"; \
	echo "  NumReplicas: $(NUM_REPLICAS)"; \
	echo "  Timeout: 100"; \
	echo "  OutputDir:  $(OUTPUT_DIR)"; \
	echo "  ReplicaScript: aptos/aptos_server.sh"; \
	echo "  # NOTE: each ClientScripts entry must have a different clientId"; \
	echo "  ClientScripts:"; \
	echo "    - aptos/aptos_client.sh 0 0 5 5"; \
	echo "    - aptos/aptos_client.sh 1 3 2 6"; \
	echo "    - aptos/aptos_client.sh 2 1 5 7"; \
	echo "    - aptos/aptos_client.sh 3 2 3 8"; \
	echo "  CleanScript: aptos/aptos_clean.sh"; \
	echo "  ReplicaParams:"; \
	for i in $$(seq 0 $$(( $(NUM_REPLICAS) - 1 ))); do \
	  echo "    - \"$$i $(BASE_DIR)\""; \
	done; \
	} > $(CONFIG)
	@cp $(CONFIG) $(OUTPUT_DIR)/aptos.yml

# -----------------------------
# Clean + run
# -----------------------------
.PHONY: clean clean-outputs

clean:
	@chmod +x $(APTOS_DIR)/aptos_clean.sh $(APTOS_DIR)/aptos_server.sh || true
	BASE_DIR=$(BASE_DIR) bash $(APTOS_DIR)/aptos_clean.sh $(BASE_DIR) || true
	@# DO NOT delete dsTest outputs here (keep all runs)

# Only run this when you explicitly want to wipe all previous runs
clean-outputs:
	rm -rf $(DSTEST_ROOT)/output || true

.PHONY: run
run:
	@chmod +x $(APTOS_DIR)/aptos_server.sh $(APTOS_DIR)/aptos_clean.sh $(APTOS_DIR)/aptos_client.sh || true
	@echo "Running dsTest with:"
	@echo "  CONFIG=$(CONFIG)"
	@echo "  BASE_DIR=$(BASE_DIR)"
	@echo "  RUST_LOG=$(RUST_LOG)"
	@echo "  LOG_LEVEL=$(LOG_LEVEL)"
	cd $(DSTEST_ROOT)/cmd/dstest && \
	  RUST_LOG="$(RUST_LOG)" LOG_LEVEL="$(LOG_LEVEL)" BASE_DIR="$(BASE_DIR)" \
	  BASE_PORT="$(BASE_PORT)" \
	  LIVENESS_TIMEOUT="$(LIVENESS_TIMEOUT)" \
	  ./main run -c "$(CONFIG)"

.PHONY: all
all: clean node-configs seeds config run-and-filter

.PHONY: filter-logs
filter-logs:
	TEST_NAME="$(TEST_NAME)" SCHED_TYPE="$(SCHED_TYPE)" \
	"$(VENV_PY)" "$(APTOS_DIR)/filter_consensus_logs.py" "$(OUTPUT_DIR)" --levels INFO,DEBUG

.PHONY: run-and-filter
run-and-filter:
	@set -e; \
	RUN_ID=$$(date +"%Y%m%d_%H%M%S"); \
	echo "[run-and-filter] RUN_ID=$$RUN_ID"; \
	{ \
		$(MAKE) RUN_ID=$$RUN_ID clean; \
		$(MAKE) RUN_ID=$$RUN_ID config; \
		$(MAKE) RUN_ID=$$RUN_ID run; \
		$(MAKE) RUN_ID=$$RUN_ID filter-logs; \
	} 2>&1 | tee "$(DSTEST_LOG)"

# -----------------------------
# Plot graphs
# -----------------------------

.PHONY: plot
plot:
	"$(VENV_PY)" $(APTOS_DIR)/aptos_plot.py \
	  --run $(OUTPUT_DIR) \
	  --config $(OUTPUT_DIR)/aptos.yml \
	  --out $(OUTPUT_DIR)/plots

.PHONY: plot-latest
plot-latest:
	@RUN_ID=$$(for r in $$(ls -t $(OUTPUT_BASE)); do \
	  if ls "$(OUTPUT_BASE)/$$r" 2>/dev/null | grep -q "^aptos-localnet_"; then echo "$$r"; break; fi; \
	done); \
	if [ -z "$$RUN_ID" ]; then echo "[plot-latest] no populated run under $(OUTPUT_BASE)"; exit 1; fi; \
	echo "[plot-latest] $(OUTPUT_BASE)/$$RUN_ID"; \
	"$(VENV_PY)" $(APTOS_DIR)/aptos_plot.py \
	  --run $(OUTPUT_BASE)/$$RUN_ID \
	  --config $(OUTPUT_BASE)/$$RUN_ID/aptos.yml \
	  --out $(OUTPUT_BASE)/$$RUN_ID/plots

# -----------------------------
# Aggregate results across runs
# -----------------------------
.PHONY: aggregate
aggregate:
	"$(VENV_PY)" $(APTOS_DIR)/aptos_aggregate.py