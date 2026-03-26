package scheduling

import (
	"math/rand"
	"time"

	"github.com/egeberkaygulcan/dstest/cmd/dstest/config"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/faults"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/network"
)

type ReplicaID int

type Partition [][]ReplicaID

// Set of (round, and a partition of P (processes))
type NetworkFaultSpec struct {
	Round     uint64
	Partition Partition
}

// Set of (round, a subset of P, and a seed)
type ProcFaultSpec struct {
	Round     uint64
	Receivers map[ReplicaID]struct{}
	Seed      int64
}

type ByzzFuzzParams struct {
	C int // # rounds with process faults
	D int // # rounds with network faults
	R int // bound on rounds with faults
}

// For network faults
// We sample from a uniform distribution of all possible partitions of the set of processes P
// by sampling from the precomputed set of partitions, since in Aptos we have small validators sets
func randomPartitionOf(nodes []ReplicaID, rng *rand.Rand) Partition {
	return nil
}

// Returns true iff a and b are in different blocks of the partition.
func isolates(p Partition, a, b ReplicaID) bool {
	return false
}

//func randomElementFrom() int

// For choosing the set of process faults
//func randomSubsetOf(s []ReplicaID, rng *rand.Rand) map[ReplicaID]struct{}

type ByzzFuzzScheduler struct {
	Scheduler
	Config         *config.Config
	NetworkManager *network.Manager

	rng    *rand.Rand
	params ByzzFuzzParams

	// sampled once per iteration
	pByz          ReplicaID // sender == pByz
	networkFaults []NetworkFaultSpec
	procFaults    []ProcFaultSpec

	// optional cached indexes for fast lookup
	networkByRound map[uint64][]NetworkFaultSpec
	procByRound    map[uint64][]ProcFaultSpec

	// logical protocol round tracking if some msgs do not expose round directly
	senderRound map[ReplicaID]uint64
}

// assert ByzzFuzz implements the Scheduler interface
var _ Scheduler = &ByzzFuzzScheduler{}

func (s *ByzzFuzzScheduler) Init(config *config.Config) {
	s.Config = config

	seed := int64(config.SchedulerConfig.Seed)
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	s.rng = rand.New(rand.NewSource(seed))

	s.params = ByzzFuzzParams{
		C: config.SchedulerConfig.Params["C"].(int),
		D: config.SchedulerConfig.Params["D"].(int),
		R: config.SchedulerConfig.Params["R"].(int),
	}

	s.Reset()
}

func (s *ByzzFuzzScheduler) Reset() {}

func (s *ByzzFuzzScheduler) NextIteration() {}

func (s *ByzzFuzzScheduler) Shutdown() {}

// Returns a random index from available messages
func (s *ByzzFuzzScheduler) Next(messages []*network.Message, faults []*faults.Fault, context faults.FaultContext) SchedulerDecision {
	// onMessage
	// Extract round
	// Extract sender / receiver
	// If (round, partition) isolates sender and receiver : drop
	// Else if sender == pByz and receiver is in a sampled proc fault set for that round : mutate and send msg
	// Else deliver unchanged
	// To introduce randomized msg delays: insert a callback function after returning NoOp, which is called after a random delay, and then send the cached message
	// Indicating msg delays can be done through a flag

	return SchedulerDecision{
		DecisionType: NoOp,
	}
}

func (s *ByzzFuzzScheduler) GetClientRequest() int {
	return -1
}

func (s *ByzzFuzzScheduler) shouldMutate(
	round uint64,
	sender ReplicaID,
	receiver ReplicaID,
) (int64, bool) {
	return 0, false
}
