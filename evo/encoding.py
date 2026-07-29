import random
import yaml


# ************************************************* #
# 				 Helpers				            #
# ************************************************* #

MSG_TYPES = [
    "ProposalMsg",
    "OptProposalMsg",
    "VoteMsg",
    "CommitMessage",
    "CommitVote",
    "CommitDecision",
    "RoundTimeoutMsg",
]


MUTATION_CATALOG = {
    "ProposalMsg": [
        "proposal_large_timestamp_future",
        "proposal_short_timestamp_future",
        "proposal_timestamps_shift_past",
        "proposal_qc_votedata_swap_with_syncinfo_highest_qc_votedata",
        "proposal_to_optimistic_proposal",
        "proposal_parent_swap_executed_state_with_hcc",
        "proposal_parent_swap_executed_state_with_hoc",
        "proposal_parent_swap_executed_state_with_hqc",
        "proposal_parent_executed_state_random_value",
        "proposal_grandparent_swap_executed_state_with_hcc",
        "proposal_grandparent_swap_executed_state_with_hoc",
        "proposal_grandparent_swap_executed_state_with_hqc",
        "proposal_grandparent_executed_state_random_value",
        "proposal_parent_inject_next_epoch_state",
        "proposal_grandparent_id_swap",
        "proposal_parent_id_swap",
        "proposal_payload_empty",
        "syncinfo_hqc_shift_rounds_up",
        "syncinfo_hqc_shift_rounds_down",
        "syncinfo_all_qcs_shift_rounds_up",
        "syncinfo_all_qcs_shift_rounds_down",
        "syncinfo_all_qcs_shift_epochs_up",
        "syncinfo_all_qcs_shift_epochs_down",
        "syncinfo_hqc_timestamp_increase",
        "syncinfo_all_qcs_downgrade_to_commit_block",
        "syncinfo_hqc_align_with_hoc",
        "syncinfo_hoc_align_with_hqc",
        "syncinfo_hcc_align_with_hoc",
        "syncinfo_qc_parent_swap_with_commit_id",
        "syncinfo_qc_executed_state_swap",
        "syncinfo_h2ctc_round_shift_up",
        "syncinfo_h2ctc_round_shift_down",
        "syncinfo_inject_h2ctc_with_hcc_qc",
        "syncinfo_inject_h2ctc_with_hoc_qc",
        "syncinfo_h2ctc_drop_to_none",
        "syncinfo_h2ctc_inner_qc_swap_to_hcc",
        "syncinfo_h2ctc_inner_qc_swap_to_hoc",
        "syncinfo_h2ctc_inner_qc_swap_to_hqc",
        "syncinfo_h2ctc_drop_bitmask_one_bit",
        "omit_mutation",
    ],
    "OptProposalMsg": [
        "optproposal_large_timestamp_future",
        "optproposal_short_timestamp_future",
        "optproposal_timestamps_shift_past",
        "optproposal_parent_version_shift_up",
        "optproposal_parent_executed_state_swap_with_syncinfo_hcc_executed_state",
        "optproposal_parent_executed_state_swap_with_syncinfo_hoc_executed_state",
        "optproposal_parent_executed_state_swap_with_syncinfo_hqc_executed_state",
        "optproposal_parent_executed_state_random_value",
        "optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hcc_executed_state",
        "optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hoc_executed_state",
        "optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hqc_executed_state",
        "optproposal_grandparent_qc_executed_state_random_value",
        "optproposal_greatgrandparent_id_swap",
        "optproposal_grandparent_qc_proposed_id_swap",
        "optproposal_payload_empty",
        "syncinfo_hqc_shift_rounds_up",
        "syncinfo_hqc_shift_rounds_down",
        "syncinfo_all_qcs_shift_rounds_up",
        "syncinfo_all_qcs_shift_rounds_down",
        "syncinfo_all_qcs_shift_epochs_up",
        "syncinfo_all_qcs_shift_epochs_down",
        "syncinfo_hqc_timestamp_increase",
        "syncinfo_all_qcs_downgrade_to_commit_block",
        "syncinfo_hqc_align_with_hoc",
        "syncinfo_hoc_align_with_hqc",
        "syncinfo_hcc_align_with_hoc",
        "syncinfo_qc_parent_swap_with_commit_id",
        "syncinfo_qc_executed_state_swap",
        "omit_mutation",
    ],
    "VoteMsg": [
        "vote_parent_version_shift_up",
        "vote_proposed_version_shift_up",
        "vote_proposed_timestamp_large_future",
        "vote_proposed_timestamp_short_future",
        "vote_timestamps_shift_past",
        "vote_parent_round_shift_down",
        "vote_proposed_id_swap",
        "vote_parent_id_swap",
        "vote_proposed_executed_state_swap_with_syncinfo_hcc_executed_state",
        "vote_proposed_executed_state_swap_with_syncinfo_hoc_executed_state",
        "vote_proposed_executed_state_swap_with_syncinfo_hqc_executed_state",
        "vote_proposed_executed_state_random_value",
        "vote_parent_executed_state_swap_with_syncinfo_hcc_executed_state",
        "vote_parent_executed_state_swap_with_syncinfo_hoc_executed_state",
        "vote_parent_executed_state_swap_with_syncinfo_hqc_executed_state",
        "vote_parent_executed_state_random_value",
        "vote_proposed_inject_next_epoch_state",
        "vote_parent_inject_next_epoch_state",
        "vote_ledger_info_commit_info_swap_with_syncinfo_hcc",
        "attach_timeout_to_vote",
        "attach_timeout_to_vote_with_hcc_qc",
        "attach_timeout_to_vote_with_hoc_qc",
        "syncinfo_hqc_shift_rounds_up",
        "syncinfo_hqc_shift_rounds_down",
        "syncinfo_all_qcs_shift_rounds_up",
        "syncinfo_all_qcs_shift_rounds_down",
        "syncinfo_all_qcs_shift_epochs_up",
        "syncinfo_all_qcs_shift_epochs_down",
        "syncinfo_hqc_timestamp_increase",
        "syncinfo_all_qcs_downgrade_to_commit_block",
        "syncinfo_hqc_align_with_hoc",
        "syncinfo_hoc_align_with_hqc",
        "syncinfo_hcc_align_with_hoc",
        "syncinfo_qc_parent_swap_with_commit_id",
        "syncinfo_qc_executed_state_swap",
        "syncinfo_h2ctc_round_shift_up",
        "syncinfo_h2ctc_round_shift_down",
        "syncinfo_inject_h2ctc_with_hcc_qc",
        "syncinfo_inject_h2ctc_with_hoc_qc",
        "syncinfo_h2ctc_drop_to_none",
        "syncinfo_h2ctc_inner_qc_swap_to_hcc",
        "syncinfo_h2ctc_inner_qc_swap_to_hoc",
        "syncinfo_h2ctc_inner_qc_swap_to_hqc",
        "syncinfo_h2ctc_drop_bitmask_one_bit",
        "omit_mutation",
    ],
    "CommitMessage": [
        "commit_swap_ack_with_nack",
        "commit_swap_nack_with_ack",
        "commit_vote_to_decision_with_full_quorum",
        "commit_vote_alter_commit_info_version_increment",
        "commit_vote_alter_commit_info_version_decrement",
        "commit_vote_alter_commit_info_id",
        "commit_vote_alter_consensus_data_hash",
        "commit_vote_alter_commit_info_executed_state_id",
        "commit_vote_alter_commit_info_inject_next_epoch_state",
        "commit_vote_alter_commit_info_clear_next_epoch_state",
        "commit_decision_alter_commit_info_version_increment",
        "commit_decision_alter_commit_info_version_decrement",
        "commit_decision_alter_consensus_data_hash",
        "commit_decision_alter_commit_info_id",
        "commit_decision_alter_commit_info_executed_state_id",
        "commit_decision_alter_commit_info_inject_next_epoch_state",
        "commit_decision_alter_commit_info_clear_next_epoch_state",
        "commit_decision_drop_bitmask_one_bit",
        "omit_mutation",
    ],
    "CommitVote": [
        "commit_vote_alter_commit_info_version_increment",
        "commit_vote_alter_commit_info_version_decrement",
        "commit_vote_alter_commit_info_id",
        "commit_vote_alter_consensus_data_hash",
        "commit_vote_alter_commit_info_executed_state_id",
        "commit_vote_alter_commit_info_inject_next_epoch_state",
        "commit_vote_alter_commit_info_clear_next_epoch_state",
        "omit_mutation",
    ],
    "CommitDecision": [
        "commit_decision_alter_commit_info_version_increment",
        "commit_decision_alter_commit_info_version_decrement",
        "commit_decision_alter_consensus_data_hash",
        "commit_decision_alter_commit_info_id",
        "commit_decision_alter_commit_info_executed_state_id",
        "commit_decision_alter_commit_info_inject_next_epoch_state",
        "commit_decision_alter_commit_info_clear_next_epoch_state",
        "commit_decision_drop_bitmask_one_bit",
        "omit_mutation",
    ],
    "RoundTimeoutMsg": [
        "roundtimeout_qc_proposed_timestamp_short_future",
        "roundtimeout_qc_proposed_timestamp_large_future",
        "roundtimeout_qc_proposed_timestamp_past",
        "roundtimeout_change_author",
        "roundtimeout_change_reason",
        "roundtimeout_qc_proposed_id_swap",
        "roundtimeout_qc_grandparent_id_swap",
        "roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hcc_executed_state",
        "roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hoc_executed_state",
        "roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hqc_executed_state",
        "roundtimeout_qc_proposed_executed_state_random_value",
        "roundtimeout_qc_proposed_inject_next_epoch_state",
        "roundtimeout_qc_proposed_clear_next_epoch_state",
        "roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hcc_executed_state",
        "roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hoc_executed_state",
        "roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hqc_executed_state",
        "roundtimeout_qc_parent_executed_state_random_value",
        "roundtimeout_qc_parent_inject_next_epoch_state",
        "roundtimeout_qc_parent_clear_next_epoch_state",
        "roundtimeout_qc_drop_bitmask_one_bit",
        "roundtimeout_qc_swap_with_syncinfo_hcc",
        "roundtimeout_qc_swap_with_syncinfo_hoc",
        "roundtimeout_qc_swap_with_syncinfo_hqc",
        "syncinfo_hqc_shift_rounds_up",
        "syncinfo_hqc_shift_rounds_down",
        "syncinfo_all_qcs_shift_rounds_up",
        "syncinfo_all_qcs_shift_rounds_down",
        "syncinfo_all_qcs_shift_epochs_up",
        "syncinfo_all_qcs_shift_epochs_down",
        "syncinfo_hqc_timestamp_increase",
        "syncinfo_all_qcs_downgrade_to_commit_block",
        "syncinfo_hqc_align_with_hoc",
        "syncinfo_hoc_align_with_hqc",
        "syncinfo_hcc_align_with_hoc",
        "syncinfo_qc_parent_swap_with_commit_id",
        "syncinfo_qc_executed_state_swap",
        "syncinfo_h2ctc_round_shift_up",
        "syncinfo_h2ctc_round_shift_down",
        "syncinfo_inject_h2ctc_with_hcc_qc",
        "syncinfo_inject_h2ctc_with_hoc_qc",
        "syncinfo_h2ctc_drop_to_none",
        "syncinfo_h2ctc_inner_qc_swap_to_hcc",
        "syncinfo_h2ctc_inner_qc_swap_to_hoc",
        "syncinfo_h2ctc_inner_qc_swap_to_hqc",
        "syncinfo_h2ctc_drop_bitmask_one_bit",
        "omit_mutation",
    ],
}


def sample_partition(num_nodes):
    # 2-partition vector of length num_nodes, with both groups non-empty
    if num_nodes < 2:
        raise ValueError("num_nodes must be at least 2")

    nodes = list(range(num_nodes))
    random.shuffle(nodes)

    cut = random.randint(1, num_nodes - 1)
    group_zero = set(nodes[:cut])

    return [0 if node in group_zero else 1 for node in range(num_nodes)]


def sample_receivers(num_nodes, pByz):
    # Non-empty receiver subset, excluding the Byzantine sender
    candidates = [node for node in range(num_nodes) if node != pByz]
    if not candidates:
        raise ValueError("no valid receivers: num_nodes must be > 1")

    random.shuffle(candidates)
    receiver_count = random.randint(1, len(candidates))

    return sorted(candidates[:receiver_count])


def sample_msg_type(exclude=None):
    choices = [m for m in MSG_TYPES if m != exclude]
    return random.choice(choices)


# For ProcessFault genes
# Mutate a process fault mutation by selecting a different mutation name 
# for the existing msg_type.
def sample_mutation_name(msg_type, exclude=None):
    choices = [m for m in MUTATION_CATALOG[msg_type] if m != exclude]
    return random.choice(choices)
    

def sample_round(r):
    return random.randint(1, r)


# When sampling rounds for NetworkFault / ProcessFault genes, they need
# to be unique across all faults, so that they do not conflict
# with each other.
def sample_round_excluding(r, existing_rounds):
    choices = [
        round for round in range(1, r+1)
        if round not in existing_rounds
    ]
    if not choices:
        raise ValueError("not enough rounds to sample unique faults")
    return random.choice(choices)


def randint_excluding(low, high, current):
    if low == high:
        return current

    value = random.randint(low, high)
    while value == current:
        value = random.randint(low, high)
    return value


# For NetworkFault genes
# Mutate a 2-partition by flipping a node from one group to the other.
def flip_one_partition_node(partition):
    zeros = partition.count(0)
    ones = partition.count(1)

    # Only flip nodes whose movement keeps both sides non-empty
    valid_indices = [
        i for i, value in enumerate(partition)
        if (value == 0 and zeros > 1) or (value == 1 and ones > 1)
    ]

    if not valid_indices:
        return partition[:]

    new_partition = partition[:]
    idx = random.choice(valid_indices)
    new_partition[idx] = 1 - new_partition[idx]
    return new_partition


# For ProcessFault genes
# Given a receiver list of a mutated message, e.g. [1,2,3]
# decide whether to add a new receiver to the list (if other nodes are available),
# to drop a current receiver (if that doesn't make the receiver list empty),
# or to replace a current receiver node id with a different one (which is not the pByz).
def mutate_receivers(receivers, num_nodes, pByz):
    candidates = [node for node in range(num_nodes) if node != pByz]
    current = set(receivers)

    ops = []
    if len(current) < len(candidates):
        ops.append("add")
    if len(current) > 1:
        ops.append("drop")
    if current and len(current) < len(candidates):
        ops.append("replace")

    if not ops:
        return sorted(current)

    op = random.choice(ops)

    if op == "add":
        current.add(random.choice([node for node in candidates if node not in current]))

    elif op == "drop":
        current.remove(random.choice(list(current)))

    elif op == "replace":
        old = random.choice(list(current))
        new = random.choice([node for node in candidates if node not in current])
        current.remove(old)
        current.add(new)

    return sorted(current)


# ************************************************* #
# 				 Encodings				            #
# ************************************************* #

class BaseEncoding:

    @staticmethod
    def sample(config):
        # return one individual
        pass

    def __init__(self):
        pass

    @staticmethod
    def mutate(ind, **kwargs):
        return (ind,)

    @staticmethod
    def mate(ind1, ind2):
        return ind1, ind2

    def to_yaml(self):
        # dump to yaml and pass to the runner function
        # or to_dict() returns a dictionary
        pass


# ************************************************* #
# 				 Genes				                #
# ************************************************* #

class PByzGene:
    def __init__(self, pByz:int):
        self.pByz = pByz
        
    @staticmethod
    def sample(config):
        return PByzGene(random.randrange(int(config["num_nodes"])))
    
    def _mutate_self(self, config):
        num_nodes = int(config["num_nodes"])
        self.pByz = randint_excluding(0, num_nodes - 1, self.pByz)

    def to_plan_value(self):
        return self.pByz


class NetworkFaultGene:
    def __init__(self, round:int, partition:list[int]):
        self.round = round
        self.partition = partition # 2-partition, only 0/1; at round, msgs crossing the two groups are delayed
        self.mutation_ops = ["round", "partition"]
        
    @staticmethod
    def sample(config, r, existing_rounds=None):
        existing_rounds = existing_rounds or set()
        return NetworkFaultGene(
            round=sample_round_excluding(r, existing_rounds),
            partition=sample_partition(int(config["num_nodes"])),
        )
    
    def _mutate_self(self, r, existing_rounds=None):
        existing_rounds = existing_rounds or set()
        op = random.choice(self.mutation_ops)

        # If we mutate the round nr, we need to make sure it does not 
        # conflict with existing rounds selected for an array of NetworkFaultGene.
        if op == "round":
            choices = [
                round for round in range(1, r+1)
                if round != self.round and round not in existing_rounds
            ]
            if choices:
                self.round = random.choice(choices)
            else:
                self.round = r + 1

        elif op == "partition":
            self.partition = flip_one_partition_node(self.partition)
    
    def to_plan_dict(self):
        return {
            "round": self.round,
            "partition": self.partition,
        }
    
    
class ProcessFaultGene:
    def __init__(self, round:int, receivers:list[int], msg_type:str, mutation_name:str):
        self.round = round
        self.receivers = receivers
        self.msg_type = msg_type
        self.mutation_name = mutation_name    # the concrete mutation for this msg_type
        
        self.mutation_ops = ["round", "receivers", "mutation_name"]
    
    @staticmethod
    def sample(config, pByz, r, existing_rounds=None):
        existing_rounds = existing_rounds or set()
        msg_type = sample_msg_type()

        return ProcessFaultGene(
            round=sample_round_excluding(r, existing_rounds),
            receivers=sample_receivers(int(config["num_nodes"]), pByz),
            msg_type=msg_type,
            mutation_name=sample_mutation_name(msg_type),
        )
    
    def _mutate_self(self, config, pByz, r, existing_rounds=None):
        existing_rounds = existing_rounds or set()
        op = random.choice(self.mutation_ops)
        
        # Mutate the round nr to a different, unique one
        # which does not conflict with existing process faults.
        if op == "round":
            choices = [
                rnd for rnd in range(1, r+1)
                if rnd != self.round and rnd not in existing_rounds
            ]
            if choices:
                self.round = random.choice(choices)
            else:
                self.round = r + 1

        elif op == "receivers":
            self.receivers = mutate_receivers(
                self.receivers,
                int(config["num_nodes"]),
                pByz,
            )

        # Keep msg_type fixed; only get a new mutation_name
        elif op == "mutation_name":
            self.mutation_name = sample_mutation_name(
                self.msg_type,
                exclude=self.mutation_name,
            )
    
    def to_plan_dict(self):
        return {
            "round": self.round,
            "receivers": self.receivers,
            "msg_type": self.msg_type,
            "mutation_name": self.mutation_name,
        }
    

# ************************************************* #
# 				 AptosEncoding				        #
# ************************************************* #

class AptosEncoding(BaseEncoding):
    def __init__(
        self, 
        pByz:PByzGene, 
        network_faults:list[NetworkFaultGene], 
        process_faults:list[ProcessFaultGene],
        r:int,
        config=None
    ):
        self.pByz = pByz
        self.network_faults = network_faults
        self.process_faults = process_faults
        self.r = r
        self.config = config
        self.mutation_ops = ["pByz", "network_faults", "process_faults"]
    
    @staticmethod
    def sample(config):
        # return one full individual
        pByz_gene = PByzGene.sample(config)
        
        num_network_faults = int(config.get("d", 1))
        num_process_faults = int(config.get("c", 1))
        r = int(config["r"])
        max_unique_rounds = int(config["r"])
        
        if num_network_faults > max_unique_rounds:
            raise ValueError("d/num_network_faults cannot exceed r when network fault rounds are unique")

        if num_process_faults > max_unique_rounds:
            raise ValueError("c/num_process_faults cannot exceed r when process fault rounds are unique")
        
        # When sampling NetworkFaults we need to make sure that rounds are unique
        # across all genes, else the faults become ambiguous.
        used_network_rounds = set()
        network_faults = []
        for _ in range(num_network_faults):
            nf = NetworkFaultGene.sample(
                config,
                r,
                existing_rounds=used_network_rounds,
            )
            network_faults.append(nf)
            used_network_rounds.add(nf.round)
        
        # When sampling ProcessFaults we need to make sure that we don't sample
        # duplicated rounds, else the faults become ambiguous.
        used_process_rounds = set()
        process_faults = []
        for _ in range(num_process_faults):
            pf = ProcessFaultGene.sample(
                config,
                pByz_gene.pByz,
                r,
                existing_rounds=used_process_rounds,
            )
            process_faults.append(pf)
            used_process_rounds.add(pf.round)
        
        ind = AptosEncoding(
            pByz = pByz_gene,
            network_faults = network_faults,
            process_faults = process_faults,
            r = r,
            config = config
        )
        
        return ind
    
    def to_plan_dict(self):
        return {
            "c": len(self.process_faults),
            "d": len(self.network_faults),
            "r": self.r,
            "pByz": self.pByz.pByz,
            "network_faults": [nf.to_plan_dict() for nf in self.network_faults],
            "process_faults": [pf.to_plan_dict() for pf in self.process_faults],
        }

    def to_yaml(self):
        return yaml.safe_dump(self.to_plan_dict(), sort_keys=False)

    def _network_rounds_except(self, idx):
        return {
            nf.round
            for i, nf in enumerate(self.network_faults)
            if i != idx
        }
    
    def _process_rounds_except(self, idx):
        return {
            pf.round
            for i, pf in enumerate(self.process_faults)
            if i != idx
        }
    
    def _valid_network_swap_pairs(self, other):
        pairs = []

        for i, nf1 in enumerate(self.network_faults):
            self_rounds_except_i = self._network_rounds_except(i)

            for j, nf2 in enumerate(other.network_faults):
                other_rounds_except_j = other._network_rounds_except(j)

                if nf2.round in self_rounds_except_i:
                    continue
                if nf1.round in other_rounds_except_j:
                    continue

                pairs.append((i, j))

        return pairs

    # Makes sure we don't swap with a pf that leads to
    # duplicated rounds across all pfs in an individual
    # or which leads to the pByz being in a receiver list
    def _valid_process_swap_pairs(self, other):
        pairs = []

        for i, pf1 in enumerate(self.process_faults):
            self_rounds_except_i = self._process_rounds_except(i)

            for j, pf2 in enumerate(other.process_faults):
                other_rounds_except_j = other._process_rounds_except(j)

                if pf2.round in self_rounds_except_i:
                    continue
                if pf1.round in other_rounds_except_j:
                    continue
                if self.pByz.pByz in pf2.receivers:
                    continue
                if other.pByz.pByz in pf1.receivers:
                    continue

                pairs.append((i, j))

        return pairs
    
    # Whenever pByz is mutated, we need to update all sets of
    # receivers in process faults to not include the newly 
    # selected pByz
    def _repair_receivers_after_pbyz_change(self):
        num_nodes = int(self.config["num_nodes"])
        pByz = self.pByz.pByz

        for pf in self.process_faults:
            pf.receivers = sorted([receiver for receiver in pf.receivers if receiver != pByz])
            if not pf.receivers:
                pf.receivers = sample_receivers(num_nodes, pByz)
    
    """ Possible mutations:
    - change pByz
    - change one network fault round
    - change one network fault partition
    - replace a nf with another non-conflicting one
    - add an entirely new non-conflicting nf
    - remove an existing nf
    - change one process fault round
    - change one process fault receivers
    - change one process fault mutation name (keep msg_type fixed)
    - replace a pf with another non-conflicting one
    - add an entirely new non-conflicting pf
    - remove an existing pf
    """
    @staticmethod
    def mutate(ind):
        available_ops = ["pByz", "network_faults", "process_faults"]
        op = random.choice(available_ops)
        
        if op == "pByz":
            ind.pByz._mutate_self(ind.config)
            ind._repair_receivers_after_pbyz_change()
        
        elif op == "network_faults":
            current_rounds = {nf.round for nf in ind.network_faults}    
            ops = ["add"]
            
            if ind.network_faults:
                ops.extend(["mutate", "replace", "remove"])
            
            op = random.choice(ops)
            
            if op == "add":
                if len(current_rounds) == ind.r:
                    ind.r += 1
                ind.network_faults.append(NetworkFaultGene.sample(
                    ind.config,
                    ind.r,
                    existing_rounds=current_rounds
                ))
            
            elif op == "remove":
                idx = random.randrange(len(ind.network_faults))
                del ind.network_faults[idx]
            
            # Mutate a part of the network fault
            elif op == "mutate":
                idx = random.randrange(len(ind.network_faults)) 
                ind.network_faults[idx]._mutate_self(
                    ind.r,
                    existing_rounds=ind._network_rounds_except(idx),
                )
                if ind.network_faults[idx].round == ind.r + 1:
                    ind.r+=1
            
            # Replace the whole network fault with a new one
            elif op == "replace":
                idx = random.randrange(len(ind.network_faults))
                ind.network_faults[idx] = NetworkFaultGene.sample(
                    ind.config,
                    ind.r,
                    existing_rounds=ind._network_rounds_except(idx),
                 )
                
        elif op == "process_faults":
            current_rounds = {nf.round for nf in ind.process_faults}                
            ops = ["add"]
            
            if ind.process_faults:
                ops.extend(["mutate", "replace", "remove"])
            
            op = random.choice(ops)
            
            if op == "add":
                if len(current_rounds) == ind.r:
                    ind.r += 1
                ind.process_faults.append(ProcessFaultGene.sample(
                    ind.config,
                    ind.pByz.pByz,
                    ind.r,
                    existing_rounds=current_rounds,
                ))
            
            elif op == "remove":
                idx = random.randrange(len(ind.process_faults))
                del ind.process_faults[idx]
                
            # Mutate a part of the process fault
            elif op == "mutate":
                idx = random.randrange(len(ind.process_faults))
                ind.process_faults[idx]._mutate_self(
                    ind.config,
                    ind.pByz.pByz,
                    ind.r,
                    existing_rounds=ind._process_rounds_except(idx)
                )
                # If round was selected to be mutated and all rounds previous rounds were used,
                # we need to increase R param
                if ind.process_faults[idx].round == ind.r + 1:
                    ind.r+=1
                
            # Replace the whole process fault with a new one
            elif op == "replace":
                idx = random.randrange(len(ind.process_faults))
                ind.process_faults[idx] = ProcessFaultGene.sample(
                    ind.config,
                    ind.pByz.pByz,
                    ind.r,
                    existing_rounds=ind._process_rounds_except(idx),
                )
                
        return (ind,)

    """Mating of two individuals:
    ind1(pByz1, nf1, pf1)
    ind2(pByz2, nf2, pf2)
    
    Possible matings are:
        - Swap nf1[i] with nf2[j]
        - Swap pf1[i] with pf2[j]
    Before swapping we need to make sure that invariants are respected.
    """
    @staticmethod
    def mate(ind1, ind2):
        available_ops = []

        network_pairs = ind1._valid_network_swap_pairs(ind2)
        if network_pairs:
            available_ops.append("network_faults")

        process_pairs = ind1._valid_process_swap_pairs(ind2)
        if process_pairs:
            available_ops.append("process_faults")

        # If nothing is possible, return unchanged parents
        if not available_ops:
            return ind1, ind2

        op = random.choice(available_ops)
            
        # Swap nf1[i] with nf2[j]
        if op == "network_faults":
            i, j = random.choice(network_pairs)
            ind1.network_faults[i], ind2.network_faults[j] = (
                ind2.network_faults[j],
                ind1.network_faults[i],
            )
            # every round must stay between [1,r]
            ind1.r = max(ind1.r, ind1.network_faults[i].round)
            ind2.r = max(ind2.r, ind2.network_faults[j].round)

        # Swap pf1[i] with pf2[j]
        elif op == "process_faults":
            i, j = random.choice(process_pairs)
            ind1.process_faults[i], ind2.process_faults[j] = (
                ind2.process_faults[j],
                ind1.process_faults[i],
            )
            ind1.r = max(ind1.r, ind1.process_faults[i].round)
            ind2.r = max(ind2.r, ind2.process_faults[j].round)
        
        return ind1, ind2
    
    """ For making sure invariants are respected across mutations/crossovers.
    For an individual:
      - Round nr in all nf genes are unique
      - Round nr in all pf genes are unique
      - Round nr is in [1, r]
      - We have 2-partition with non-empty sets
      - We have non-empty pf receivers
      - PByz does not appear in any pf receivers
      - The msg_type of a pf is valid and appears in the catalog
      - The mutation_name of a pf is valid and appears in the catalog of possible mutations for this msg_type
    """
    def validate(self):
        assert len({nf.round for nf in self.network_faults}) == len(self.network_faults)
        assert len({pf.round for pf in self.process_faults}) == len(self.process_faults)
        assert self.r >= 1
        num_nodes = int(self.config["num_nodes"])
        assert 0 <= self.pByz.pByz < num_nodes

        for nf in self.network_faults:
            assert len(nf.partition) == int(self.config["num_nodes"])
            assert set(nf.partition) <= {0, 1}
            assert 0 in nf.partition and 1 in nf.partition
            assert(nf.round <= self.r)
            assert(nf.round >= 1)

        # nonempty, unique, receivers with valid node ids that do not contain pbyz
        for pf in self.process_faults:
            assert pf.receivers
            assert len(pf.receivers) == len(set(pf.receivers))
            assert all(isinstance(receiver, int) for receiver in pf.receivers)
            assert all(0 <= receiver < num_nodes for receiver in pf.receivers)
            assert self.pByz.pByz not in pf.receivers
            assert(pf.round <= self.r)
            assert(pf.round >= 1)
            assert pf.msg_type in MSG_TYPES
            assert pf.mutation_name in MUTATION_CATALOG[pf.msg_type]


if __name__ == "__main__":
    config = {"num_nodes": 6, "r": 6, "c": 2, "d": 1}
    
    inds = []
    for i in range(4):
        ind = AptosEncoding.sample(config)
        inds.append(ind)
        print(f"======== individual {i} ======== ")
        print(ind.to_yaml())
        
    # test mutation
    ind = inds[0]
    print("**** before mutation ****")
    print(ind.to_yaml())
    AptosEncoding.mutate(ind)
    print("**** after mutation ****")
    print(ind.to_yaml())
    ind.validate()
    
    # test crossover
    ind1, ind2 = inds[0], inds[1]
    print("**** before crossover ****")
    print("ind1:")
    print(ind1.to_yaml())
    print("ind2:")
    print(ind2.to_yaml())
    AptosEncoding.mate(ind1, ind2)
    print("**** after crossover ****")
    print("ind1:")
    print(ind1.to_yaml())
    print("ind2:")
    print(ind2.to_yaml())   
    ind1.validate()
    ind2.validate()