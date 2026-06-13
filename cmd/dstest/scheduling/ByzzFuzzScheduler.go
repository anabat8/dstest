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

type NetworkFaults struct {
	Faults        []NetworkFaultSpec
	mu            *sync.Mutex
	recoveryTimer *time.Timer
}

func (f *NetworkFaults) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Faults = make([]NetworkFaultSpec, 0)
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
	C             int // no of rounds with process faults
	D             int // no of rounds with network faults
	R             int // bound on rounds with faults
	Recovery_Time int // time until network heals and dropped messages are sent, in seconds
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

func isNetworkFault(round aptos.Round, sender ReplicaID, receiver ReplicaID, faults *NetworkFaults) bool {
	faults.mu.Lock()
	defer faults.mu.Unlock()
	for _, f := range faults.Faults {
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

/*
Stores the dropped messages due to network partitions.
When the network heals, these messages will be sent.
*/
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
	AvailableClients []int
	ClientDone       chan struct{}

	rng    *rand.Rand
	params ByzzFuzzParams

	// sampled once per iteration
	pByz          ReplicaID // sender == pByz
	networkFaults NetworkFaults
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
	s.rng = rand.New(rand.NewSource(seed))

	s.params = ByzzFuzzParams{
		C:             config.SchedulerConfig.Params["c"].(int),
		D:             config.SchedulerConfig.Params["d"].(int),
		R:             config.SchedulerConfig.Params["r"].(int),
		Recovery_Time: config.SchedulerConfig.Params["recovery_seconds"].(int),
	}
	s.delayStore = NewDelayMessagesStore()

	s.NumClientTypes = len(config.ProcessConfig.ClientScripts)

	s.AvailableClients = make([]int, s.NumClientTypes)
	for i := range s.NumClientTypes {
		s.AvailableClients[i] = i
	}
	s.ClientDone = make(chan struct{}, 1)
	s.ClientDone <- struct{}{}

	s.Log = log.New(os.Stdout, "[ByzzFuzz Scheduler] ", log.LstdFlags)

	s.iteration = 0
	s.networkFaults = NetworkFaults{make([]NetworkFaultSpec, 0), &sync.Mutex{}, time.NewTimer(0)}
}

func (s *ByzzFuzzScheduler) Reset() {
	seed := int64(s.Config.SchedulerConfig.Seed)
	s.rng = rand.New(rand.NewSource(seed))
}

func (s *ByzzFuzzScheduler) NextIteration() {
	s.iteration++

	<-s.ClientDone
	s.AvailableClients = make([]int, s.NumClientTypes)
	for i := range s.NumClientTypes {
		s.AvailableClients[i] = i
	}
	s.ClientDone <- struct{}{}

	s.delayStore = NewDelayMessagesStore()

	// sample network faults for the iteration
	s.networkFaults.recoveryTimer.Stop()
	s.networkFaults.mu.Lock()

	s.networkFaults.Faults = make([]NetworkFaultSpec, s.params.D)
	for i := 0; i < s.params.D; i++ {
		round := aptos.Round(s.rng.Intn(s.params.R))
		partition := NewPartition(ReplicaIDs(s.NetworkManager.ReplicaIds), s.rng)
		s.networkFaults.Faults[i] = NetworkFaultSpec{
			Round:     round,
			Partition: partition,
		}
	}
	s.networkFaults.mu.Unlock()

	// Schedule network recovery after some time (e.g.: 30 or 60 seconds)
	s.networkFaults.recoveryTimer = time.AfterFunc(time.Duration(s.params.Recovery_Time)*time.Second, s.OnRecoveryStart)

	s.Log.Printf("Sampled network faults for iteration: %v\n", s.networkFaults.Faults)

	s.pByz = ReplicaID(s.NetworkManager.ReplicaIds[s.rng.Intn(len(s.NetworkManager.ReplicaIds))])

	s.Log.Printf("Chosen Byzantine sender for iteration: %d\n", s.pByz)

	// sample process faults for the iteration
	s.procFaults = make([]ProcFaultSpec, s.params.C)
	for i := 0; i < s.params.C; i++ {
		round := aptos.Round(s.rng.Intn(s.params.R))
		procs := randomSubsetOf(s.NetworkManager.ReplicaIds, s.rng)
		byzzfuzzSeed := s.rng.Int63()

		s.procFaults[i] = ProcFaultSpec{
			Round:     round,
			Receivers: procs,
			Seed:      byzzfuzzSeed,
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
			"message", "mutated_message", "mutation_name", "mutation_method", "message_id",
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
	s.networkFaults.recoveryTimer.Stop()
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

	if isNetworkFault(round, sender, receiver, &s.networkFaults) {
		// temporary drop the message (remove from queue)
		// msg will be resent with delay (after network recovers)
		s.Log.Printf("Dropping message from %d to %d in round %d with msg id %d due to network fault\n", sender, receiver, round, chosenMsg.MessageId)
		s.delayStore.SetDelayed(
			chosenMsg.MessageId,
			time.Duration(s.params.Recovery_Time)*time.Second,
			chosenMsg.SendMessage,
		)
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
		// mutate the message by changing the payload
		ogMsg := c.String()

		mutation, err := s.Mutator.Mutate(c, seedProcFault) //msg is mutated in place

		s.Log.Printf(
			"Mutating message from %d to %d in round %d with msg id %d\n and mutation %s",
			sender, receiver, round, chosenMsg.MessageId, mutation.Name,
		)

		if err != nil {
			s.Log.Printf("Error in mutation %s: %v", mutation.Name, err)
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
				mutation.Name,
				string(mutation.Method),
				fmt.Sprintf("%d", chosenMsg.MessageId),
			})
			s.mutationLog.Flush()
		}

		if mutation.ShouldOmitSending() {
			return SchedulerDecision{
				DecisionType: DropMessage,
				Index:        index,
			}
		}

		return SchedulerDecision{
			DecisionType:   DeliverMutatedMessage,
			Index:          index,
			MutatedMessage: chosenMsg,
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
Pops the next client-script index off AvailableClients (FIFO), or returns -1
if there's nothing to fire right now.

AvailableClients is guarded by ClientDone, a 1-token
buffered channel acting as a mutex. Only the goroutine holding the token may
mutate the slice. This guarantees at most one client worker is in flight at
a time. Without it, concurrent workers sharing the same client account
race on its sequence number.

Returns -1 when any of:
  - no client scripts are configured;
  - the mutex is held (another worker in flight);
  - all scripts already fired successfully: in this
    case we return the token immediately so NextIteration isn't starved.

A popped script that exits with code 2 (target block height not yet reached)
is re-prepended by ClientRequestDone for a later retry. A script that exits
successfully is dropped; each script fires at most once per iter, in order.
*/
func (s *ByzzFuzzScheduler) GetClientRequest() int {
	if s.NumClientTypes == 0 {
		return -1
	}

	select {
	case <-s.ClientDone:
		if len(s.AvailableClients) == 0 {
			s.ClientDone <- struct{}{}
			return -1
		}
		clientId := s.AvailableClients[0]
		s.AvailableClients = s.AvailableClients[1:]
		return clientId
	default:
		return -1
	}
}

/*
Returns the mutex token after a worker finishes. If the worker exited with code 2
(height not reached), the client script index is re-prepended to AvailableClients for a later retry.
On any other exit, the slot is dropped (each script fires at most once per iter).
Called by TestEngine.Run from a goroutine waiting on the worker's done-channel.
*/
func (s *ByzzFuzzScheduler) ClientRequestDone(client, exitCode int) {
	if exitCode == 2 {
		s.AvailableClients = append([]int{client}, s.AvailableClients...)
	}
	s.ClientDone <- struct{}{}
}

/*
When we have network partitions, some messages are dropped (not sent currently).
When the network heals, these messages should be sent.
We need to clear network faults immediately when recovery starts, so that new messages aren't dropped.
Mutations can still happen after recovery starts.
Network recovery happens after Recovery_Time param seconds (e.g.: 60 or 30 seconds) since the iteration starts.
*/
func (s *ByzzFuzzScheduler) OnRecoveryStart() {
	s.Log.Println("Network heal started, releasing held (dropped) messages")
	s.networkFaults.Reset()
}

func (s *ByzzFuzzScheduler) SetNetworkManager(networkManager *network.Manager) {
	s.NetworkManager = networkManager
}
