package scheduling

import (
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egeberkaygulcan/dstest/cmd/dstest/config"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/faults"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/network"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
)

type ReplicaID int

func ReplicaIDs(nodes []int) []ReplicaID {
	out := make([]ReplicaID, len(nodes))
	for i, n := range nodes {
		out[i] = ReplicaID(n)
	}
	return out
}

type Partition struct {
	blocks int
	of     map[ReplicaID]int
}

/*
For network faults
We sample from a uniform distribution of all possible partitions of the set of processes P
by sampling from the precomputed set of partitions, since in Aptos we have small validators sets
*/
func NewPartition(nodes []ReplicaID, rng *rand.Rand) Partition {
	p := Partition{
		blocks: 0,
		of:     make(map[ReplicaID]int),
	}

	for _, n := range nodes {
		pId := rng.Intn(p.blocks + 1)
		if pId == p.blocks {
			p.blocks++
		}
		p.of[n] = pId
	}

	return p
}

func (p Partition) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("No of partitions: %d\n", p.blocks))
	for n, b := range p.of {
		sb.WriteString(fmt.Sprintf("%d -> %d\n", n, b))
	}
	return sb.String()
}

func (p Partition) Isolates(a, b ReplicaID) bool {
	return p.of[a] != p.of[b]
}

/*
Set of (round, and a partition of P (processes))
*/
type NetworkFaultSpec struct {
	Round     aptos.Round
	Partition Partition
}

/*
Set of (round, a subset of P, and a seed)
*/
type ProcFaultSpec struct {
	Round     aptos.Round            // round number in which the faulty process sends mutated messages (or omits them)
	Receivers map[ReplicaID]struct{} // set of receivers of the mutated messages
	Seed      int64                  // random seed to determine how to mutate the message
}

type ByzzFuzzParams struct {
	C int // no of rounds with process faults
	D int // no of rounds with network faults
	R int // bound on rounds with faults
}

/*
For choosing the set of process faults uniformly at random
*/
func randomSubsetOf(s []int, rng *rand.Rand) map[ReplicaID]struct{} {
	out := make(map[ReplicaID]struct{})

	if len(s) == 0 {
		return out
	}

	for _, x := range s {
		if rng.Intn(2) == 1 {
			out[ReplicaID(x)] = struct{}{}
		}
	}

	if len(out) == 0 {
		x := s[rng.Intn(len(s))]
		out[ReplicaID(x)] = struct{}{}
	}

	return out
}

func isNetworkFault(round aptos.Round, sender ReplicaID, receiver ReplicaID, faults []NetworkFaultSpec) bool {
	for _, f := range faults {
		if f.Round == round && f.Partition.Isolates(sender, receiver) {
			return true
		}
	}
	return false
}

func isProcFault(round aptos.Round, receiver ReplicaID, faults []ProcFaultSpec) (int64, bool) {
	for _, f := range faults {
		if f.Round == round {
			if _, ok := f.Receivers[receiver]; ok {
				return f.Seed, true
			}
		}
	}
	return 0, false
}

type DelayMessagesStore struct {
	// if message with MessageId is delayed
	isDelayed map[uint64]bool
	mu        sync.Mutex
}

func NewDelayMessagesStore() *DelayMessagesStore {
	return &DelayMessagesStore{
		isDelayed: make(map[uint64]bool),
	}
}

func (store *DelayMessagesStore) SetDelayed(messageId uint64, delayTime time.Duration, sendFunc func()) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.isDelayed[messageId] == true {
		return
	}
	store.isDelayed[messageId] = true
	go func() {
		time.Sleep(delayTime)
		//sendFunc(messageId)
		sendFunc()
		store.mu.Lock()
		defer store.mu.Unlock()
		delete(store.isDelayed, messageId)
	}()
}

type ByzzFuzzScheduler struct {
	Scheduler
	Config           *config.Config
	NetworkManager   *network.Manager
	Mutator          network.Mutator
	Log              *log.Logger
	NumClientTypes   int
	AvailableClients chan int

	RequestQuota             int
	ClientRequestProbability float64

	rng    *rand.Rand
	params ByzzFuzzParams

	// sampled once per iteration
	pByz          ReplicaID // sender == pByz
	networkFaults []NetworkFaultSpec
	procFaults    []ProcFaultSpec

	delayStore *DelayMessagesStore

	mutationLog  *csv.Writer
	mutationFile *os.File
	iteration    int
}

// assert ByzzFuzz implements the Scheduler interface
var _ Scheduler = &ByzzFuzzScheduler{}

func (s *ByzzFuzzScheduler) Init(config *config.Config) {
	s.Config = config
	s.Mutator = network.NewAptosMutator(aptos.CollectValidatorKeysByAuthor())

	seed := int64(config.SchedulerConfig.Seed)
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	s.rng = rand.New(rand.NewSource(seed))

	s.params = ByzzFuzzParams{
		C: config.SchedulerConfig.Params["c"].(int),
		D: config.SchedulerConfig.Params["d"].(int),
		R: config.SchedulerConfig.Params["r"].(int),
	}
	s.delayStore = NewDelayMessagesStore()

	s.RequestQuota = config.SchedulerConfig.ClientRequests
	s.NumClientTypes = len(config.ProcessConfig.ClientScripts)

	// Handle both int and float64 for client_request_probability
	if prob, ok := config.SchedulerConfig.Params["client_request_probability"].(float64); ok {
		s.ClientRequestProbability = prob
	} else if probInt, ok := config.SchedulerConfig.Params["client_request_probability"].(int); ok {
		s.ClientRequestProbability = float64(probInt)
	} else {
		s.ClientRequestProbability = 0.05 // default
	}

	s.AvailableClients = make(chan int, s.NumClientTypes)
	for i := range s.NumClientTypes {
		s.AvailableClients <- i
	}

	s.Log = log.New(os.Stdout, "[ByzzFuzz Scheduler] ", log.LstdFlags)

	s.iteration = 0
}

func (s *ByzzFuzzScheduler) Reset() {
	s.RequestQuota = s.Config.SchedulerConfig.ClientRequests
	seed := int64(s.Config.SchedulerConfig.Seed)
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	s.rng = rand.New(rand.NewSource(seed))
}

func (s *ByzzFuzzScheduler) NextIteration() {
	s.iteration++
	s.RequestQuota = s.Config.SchedulerConfig.ClientRequests

	// sample network faults for the iteration
	s.networkFaults = make([]NetworkFaultSpec, s.params.D)
	for i := 0; i < s.params.D; i++ {
		round := aptos.Round(s.rng.Intn(s.params.R))
		partition := NewPartition(ReplicaIDs(s.NetworkManager.ReplicaIds), s.rng)
		s.networkFaults[i] = NetworkFaultSpec{
			Round:     round,
			Partition: partition,
		}
	}

	s.Log.Printf("Sampled network faults for iteration: %v\n", s.networkFaults)

	s.pByz = ReplicaID(s.NetworkManager.ReplicaIds[s.rng.Intn(len(s.NetworkManager.ReplicaIds))])

	s.Log.Printf("Chosen Byzantine sender for iteration: %d\n", s.pByz)

	// sample process faults for the iteration
	s.procFaults = make([]ProcFaultSpec, s.params.C)
	for i := 0; i < s.params.C; i++ {
		round := aptos.Round(s.rng.Intn(s.params.R))
		procs := randomSubsetOf(s.NetworkManager.ReplicaIds, s.rng)
		procsSeed := time.Now().UnixNano()
		s.procFaults[i] = ProcFaultSpec{
			Round:     round,
			Receivers: procs,
			Seed:      procsSeed,
		}
	}

	s.Log.Printf("Sampled process faults for iteration: %v\n", s.procFaults)

	// create a new mutation log csv for each iteration
	if s.mutationFile != nil {
		s.mutationLog.Flush()
		s.mutationFile.Close()
	}

	path := filepath.Join(s.Config.ProcessConfig.OutputDir,
		fmt.Sprintf("%s_%s_%d",
			s.Config.TestConfig.Name,
			s.Config.SchedulerConfig.Type,
			s.iteration),
		"mutations.csv")

	os.MkdirAll(filepath.Dir(path), os.ModePerm)

	f, err := os.Create(path)
	if err != nil {
		s.Log.Printf("Failed to create mutations CSV: %v", err)
	} else {
		s.mutationFile = f
		s.mutationLog = csv.NewWriter(f)
		s.mutationLog.Write([]string{
			"sender_id", "receiver_id", "timestamp", "round",
			"message", "mutated_message", "mutation_method", "message_id",
		})
	}
}

func (s *ByzzFuzzScheduler) Shutdown() {
	if s.mutationLog != nil {
		s.mutationLog.Flush()
	}
	if s.mutationFile != nil {
		s.mutationFile.Close()
	}
}

/*
Returns a random index from available messages
*/
func (s *ByzzFuzzScheduler) Next(messages []*network.Message, faults []*faults.Fault, context faults.FaultContext) SchedulerDecision {
	// if there are no messages, return a noOp
	if len(messages) == 0 {
		s.Log.Println("No messages to schedule, returning NoOp")
		return SchedulerDecision{
			DecisionType: NoOp,
		}
	}

	index := s.rng.Intn(len(messages))
	chosenMsg := messages[index]

	c, ok := chosenMsg.Payload.(aptos.IConsensusMessage)
	if !ok {
		s.Log.Printf("Message payload is not an Aptos consensus message: %T, returning NoOp\n", chosenMsg.Payload)
		return SchedulerDecision{
			DecisionType: NoOp,
		}
	}

	round := c.GetRound()
	timestamp := c.GetTimestamp()
	sender := ReplicaID(chosenMsg.Sender)
	receiver := ReplicaID(chosenMsg.Receiver)

	if isNetworkFault(round, sender, receiver, s.networkFaults) {
		// do nothing, drop the message (remove from queue)
		s.Log.Printf("Dropping message from %d to %d in round %d with msg id %d due to network fault\n", sender, receiver, round, chosenMsg.MessageId)
		return SchedulerDecision{
			DecisionType: DropMessage,
			Index:        index,
		}
	} else if sender == s.pByz {
		seedProcFault, ok := isProcFault(round, receiver, s.procFaults)
		if !ok {
			// send original message (without mutation)
			s.Log.Printf("Sending message from %d to %d in round %d with msg id %d\n", sender, receiver, round, chosenMsg.MessageId)
			return SchedulerDecision{
				DecisionType: SendMessage,
				Index:        index,
			}
		}
		// mutate the message by adding a random delay, or by changing the payload
		shouldDelay := (s.rng.Intn(2) == 0)
		if shouldDelay {
			s.Log.Printf("Delaying message from %d to %d in round %d with msg id %d\n", sender, receiver, round, chosenMsg.MessageId)
			// add random delay
			s.delayStore.SetDelayed(
				chosenMsg.MessageId,
				time.Duration(5*time.Second),
				chosenMsg.SendMessage,
			)
			return SchedulerDecision{
				DecisionType: DropMessage,
				Index:        index,
			}
		} else {
			ogMsg := c.String()

			mname, err := s.Mutator.Mutate(c, seedProcFault) //msg is mutated in place

			s.Log.Printf("Mutating message from %d to %d in round %d with msg id %d\n and mutation %s", sender, receiver, round, chosenMsg.MessageId, mname)

			if err != nil {
				s.Log.Printf("Error in mutation %s: %v", mname, err)
				return SchedulerDecision{
					DecisionType: NoOp,
				}
			}
			if s.mutationLog != nil {
				s.mutationLog.Write([]string{
					fmt.Sprintf("%d", sender),
					fmt.Sprintf("%d", receiver),
					fmt.Sprintf("%d", timestamp),
					fmt.Sprintf("%d", round),
					ogMsg,
					c.String(),
					mname,
					fmt.Sprintf("%d", chosenMsg.MessageId),
				})
				s.mutationLog.Flush()
			}
			return SchedulerDecision{
				DecisionType:   DeliverMutatedMessage,
				Index:          index,
				MutatedMessage: chosenMsg,
			}
		}
	} else {
		// send original message (without mutation)
		s.Log.Printf("Sending message from %d to %d in round %d with msg id %d\n", sender, receiver, round, chosenMsg.MessageId)
		return SchedulerDecision{
			DecisionType: SendMessage,
			Index:        index,
		}
	}
}

/*
Decides whether to fire a client request this scheduler step. Returns the
index of a client-script slot (an entry of config.ProcessConfig.ClientScripts)
or -1 to skip.

Returns -1 if any of:
  - request quota for the iteration is exhausted
  - no client scripts are configured
  - the Bernoulli trial against ClientRequestProbability fails
  - all client slots are currently in flight (pool empty)

Quota is decremented only on a successful pop, so failed Bernoulli trials and
empty-pool skips don't consume the iteration's request budget.

AvailableClients is a queue of client-script indices [0..NumClientTypes).
GetClientRequest pops a slot (a client script) from the queue non-blockingly when the scheduler decides to.
ClientRequestDone returns the slot once the engine's worker goroutine signals completion.

The pool guarantees no two in-flight workers share the same client identity (and client script).
Without this, concurrent workers signing with the same account race on its
sequence number when sending batches of txs.
*/
func (s *ByzzFuzzScheduler) GetClientRequest() int {
	if s.RequestQuota <= 0 {
		return -1
	}
	if s.NumClientTypes == 0 {
		return -1
	}

	r := s.rng.Float64()
	if r <= s.ClientRequestProbability {
		select {
		case clientId := <-s.AvailableClients:
			s.RequestQuota--
			return clientId
		default:
			return -1
		}
	}
	return -1
}

/*
Returns a client-script slot to the pool once its worker has finished. Called
by TestEngine.Run from a goroutine waiting on the worker's done-channel.
*/
func (s *ByzzFuzzScheduler) ClientRequestDone(client int) {
	s.AvailableClients <- client
}

func (s *ByzzFuzzScheduler) SetNetworkManager(networkManager *network.Manager) {
	s.NetworkManager = networkManager
}
