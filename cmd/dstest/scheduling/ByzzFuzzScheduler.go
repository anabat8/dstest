package scheduling

import (
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
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

/* ************************************************* */
/* 					Network Faults					 */
/* ************************************************* */

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

func (n NetworkFaultSpec) String() string {
	return fmt.Sprintf(
		"network_fault{round=%d partition=%s}",
		n.Round,
		n.Partition,
	)
}

type NetworkFaults struct {
	Sampler             NetworkFaultSampler
	Faults              []NetworkFaultSpec
	RecoveryTimeSeconds int
	mu                  *sync.Mutex
	recoveryTimer       *time.Timer
}

func (faults *NetworkFaults) IsNetworkFault(round aptos.Round, sender ReplicaID, receiver ReplicaID) bool {
	faults.mu.Lock()
	defer faults.mu.Unlock()
	for _, f := range faults.Faults {
		if f.Round == round && f.Partition.Isolates(sender, receiver) {
			return true
		}
	}
	return false
}

func (f *NetworkFaults) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Faults = make([]NetworkFaultSpec, 0)
}

func (f *NetworkFaults) NextIteration(cb func()) {
	f.recoveryTimer.Stop()
	f.mu.Lock()
	f.Faults = f.Sampler.SampleNetworkFaults()
	f.mu.Unlock()

	// Schedule network recovery after some time (e.g.: 30 or 60 seconds)
	f.recoveryTimer = time.AfterFunc(time.Duration(f.RecoveryTimeSeconds)*time.Second, cb)
}

/* ************************************************* */
/* 						Sampler						 */
/* ************************************************* */
type NetworkFaultSampler interface {
	SampleNetworkFaults() []NetworkFaultSpec
}

type PByzSampler interface {
	SamplePByz() ReplicaID
}

type ProcessFaultSampler interface {
	SampleProcessFaults() []ProcFaultSpec
}

type Sampler interface {
	NetworkFaultSampler
	PByzSampler
	ProcessFaultSampler
}

/* ************************************************* */
/* 					Random Sampler					 */
/* ************************************************* */

type RandomSampler struct {
	C          int
	D          int
	R          int
	Rng        *rand.Rand
	ReplicaIds []int
}

func (s *RandomSampler) SampleNetworkFaults() []NetworkFaultSpec {
	// no evolutionary fault plan, sample randomly
	faults := make([]NetworkFaultSpec, s.D)
	for i := 0; i < s.D; i++ {
		round := aptos.Round(s.Rng.Intn(s.R))
		partition := NewPartition(ReplicaIDs(s.ReplicaIds), s.Rng)
		faults[i] = NetworkFaultSpec{
			Round:     round,
			Partition: partition,
		}
	}
	return faults
}

func (s *RandomSampler) SamplePByz() ReplicaID {
	return ReplicaID(s.ReplicaIds[s.Rng.Intn(len(s.ReplicaIds))])
}

func (s *RandomSampler) SampleProcessFaults() []ProcFaultSpec {
	procFaults := make([]ProcFaultSpec, s.C)
	for i := 0; i < s.C; i++ {
		round := aptos.Round(s.Rng.Intn(s.R))
		procs := randomSubsetOf(s.ReplicaIds, s.Rng)
		byzzfuzzSeed := s.Rng.Int63()

		procFaults[i] = ProcFaultSpec{
			Round:     round,
			Receivers: procs,
			Seed:      byzzfuzzSeed,
		}
	}
	return procFaults
}

/* ************************************************* */
/* 				Evolutionary Sampler				 */
/* ************************************************* */

type EvoNetworkSampler struct {
}

/* ************************************************* */
/* 				   Process Faults				     */
/* ************************************************* */

/*
Set of (round, a subset of P, and a seed)
*/
type ProcFaultSpec struct {
	Round     aptos.Round            // round number in which the faulty process sends mutated messages (or omits them)
	Receivers map[ReplicaID]struct{} // set of receivers of the mutated messages
	Seed      int64                  // random seed to determine how to mutate the message
}

func (p ProcFaultSpec) String() string {
	receivers := make([]int, 0, len(p.Receivers))
	for receiver := range p.Receivers {
		receivers = append(receivers, int(receiver))
	}
	sort.Ints(receivers)

	return fmt.Sprintf(
		"process_fault{round=%d receivers=%v seed=%d}",
		p.Round,
		receivers,
		p.Seed,
	)
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

/* ************************************************* */
/* 				 Delay Message Store				 */
/* ************************************************* */

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

/* ************************************************* */
/* 				    Mutation Writer				     */
/* ************************************************* */
type MutationWriter struct {
	*os.File
	*csv.Writer
}

func (m *MutationWriter) Close() error {
	if m.Writer != nil {
		m.Writer.Flush()
	}
	if m.File != nil {
		return m.File.Close()
	}
	return nil
}

func (m *MutationWriter) NextIteration(config *config.Config, iteration int) {
	m.Close()
	path := filepath.Join(config.ProcessConfig.OutputDir,
		fmt.Sprintf("%s_%s_%d",
			config.TestConfig.Name,
			config.SchedulerConfig.Type,
			iteration),
		"mutations.csv")

	os.MkdirAll(filepath.Dir(path), os.ModePerm)

	f, err := os.Create(path)
	if err != nil {
		panic(fmt.Errorf("Failed to create mutations CSV: %v", err))
	} else {
		m.File = f
		m.Writer = csv.NewWriter(f)
		m.Writer.Write([]string{
			"sender_id", "receiver_id", "timestamp", "round",
			"message", "mutated_message", "mutation_name", "mutation_method", "message_id",
		})
	}
}

func (m *MutationWriter) WriteMutation(row []string) {
	if m.Writer != nil {
		m.Writer.Write(row)
		m.Writer.Flush()
	}
}

func NewMutationWriter() *MutationWriter {
	return new(MutationWriter)
}

/* ************************************************* */
/* 				        Clients			    		 */
/* ************************************************* */
type Clients struct {
	NumClientTypes   int
	AvailableClients []int
	ClientDone       chan struct{}
}

func NewClients(n int) *Clients {
	clients := new(Clients)
	clients.NumClientTypes = n

	clients.AvailableClients = make([]int, n)
	for i := range n {
		clients.AvailableClients[i] = i
	}
	clients.ClientDone = make(chan struct{}, 1)
	clients.ClientDone <- struct{}{}
	return clients
}

func (c *Clients) NextIteration() {
	<-c.ClientDone
	c.AvailableClients = make([]int, c.NumClientTypes)
	for i := range c.NumClientTypes {
		c.AvailableClients[i] = i
	}
	c.ClientDone <- struct{}{}
}

/*
Returns the mutex token after a worker finishes. If the worker exited with code 2
(height not reached), the client script index is re-prepended to AvailableClients for a later retry.
On any other exit, the slot is dropped (each script fires at most once per iter).
Called by TestEngine.Run from a goroutine waiting on the worker's done-channel.
*/
func (c *Clients) ClientRequestDone(client, exitCode int) {
	if exitCode == 2 {
		c.AvailableClients = append([]int{client}, c.AvailableClients...)
	}
	c.ClientDone <- struct{}{}
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
func (c *Clients) GetClientRequest() int {
	if c.NumClientTypes == 0 {
		return -1
	}

	select {
	case <-c.ClientDone:
		if len(c.AvailableClients) == 0 {
			c.ClientDone <- struct{}{}
			return -1
		}
		clientId := c.AvailableClients[0]
		c.AvailableClients = c.AvailableClients[1:]
		return clientId
	default:
		return -1
	}
}

/* ************************************************* */
/* 				   ByzzFuzz Scheduler				 */
/* ************************************************* */

type ByzzFuzzParams struct {
	C             int // no of rounds with process faults
	D             int // no of rounds with network faults
	R             int // bound on rounds with faults
	Recovery_Time int // time until network heals and dropped messages are sent, in seconds
}

type ByzzFuzzScheduler struct {
	*config.Config
	*MutationWriter
	*Clients
	Sampler

	Mutator network.Mutator
	Log     *log.Logger

	rng    *rand.Rand
	params ByzzFuzzParams

	// sampled once per iteration
	pByz          ReplicaID // sender == pByz
	networkFaults NetworkFaults
	procFaults    []ProcFaultSpec

	delayStore *DelayMessagesStore

	iteration int
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

	path := config.SchedulerConfig.Params["evo_fault_plan"].(string)

	if path != "" {
		// init evo sampler

	} else {
		replicaIds := make([]int, s.Config.ProcessConfig.NumReplicas)
		for i := range replicaIds {
			replicaIds[i] = i
		}

		s.Sampler = &RandomSampler{
			C:          s.params.C,
			D:          s.params.D,
			R:          s.params.R,
			Rng:        s.rng,
			ReplicaIds: replicaIds,
		}
	}

	s.MutationWriter = NewMutationWriter()
	s.delayStore = NewDelayMessagesStore()

	s.Clients = NewClients(len(config.ProcessConfig.ClientScripts))

	s.Log = log.New(os.Stdout, "[ByzzFuzz Scheduler] ", log.LstdFlags)

	s.iteration = 0
	s.networkFaults = NetworkFaults{
		Sampler:             s.Sampler,
		Faults:              make([]NetworkFaultSpec, 0),
		mu:                  &sync.Mutex{},
		recoveryTimer:       time.NewTimer(0),
		RecoveryTimeSeconds: s.params.Recovery_Time,
	}

	s.networkFaults.NextIteration(s.OnRecoveryStart)
	s.Log.Printf("Sampled network faults for iteration: %v\n", s.networkFaults.Faults)
	s.pByz = s.SamplePByz()
	s.Log.Printf("Chosen Byzantine sender for iteration: %d\n", s.pByz)
	s.procFaults = s.SampleProcessFaults()
	s.Log.Printf("Sampled process faults for iteration: %v\n", s.procFaults)
	s.MutationWriter.NextIteration(s.Config, s.iteration)
}

func (s *ByzzFuzzScheduler) NextIteration() {
	s.iteration++
	s.Clients.NextIteration()
	s.delayStore = NewDelayMessagesStore()

	s.networkFaults.NextIteration(s.OnRecoveryStart)

	s.Log.Printf("Sampled network faults for iteration: %v\n", s.networkFaults.Faults)
	s.pByz = s.SamplePByz()
	s.Log.Printf("Chosen Byzantine sender for iteration: %d\n", s.pByz)

	s.procFaults = s.SampleProcessFaults()
	s.Log.Printf("Sampled process faults for iteration: %v\n", s.procFaults)

	// create a new mutation log csv for each iteration
	s.MutationWriter.NextIteration(s.Config, s.iteration)
}

func (s *ByzzFuzzScheduler) Shutdown() {
	s.MutationWriter.Close()
	s.networkFaults.recoveryTimer.Stop()
}

func (s *ByzzFuzzScheduler) Reset() {
	seed := int64(s.Config.SchedulerConfig.Seed)
	s.rng = rand.New(rand.NewSource(seed))
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

	c, isConsensus := chosenMsg.Payload.(aptos.IConsensusMessage)
	if !isConsensus {
		s.Log.Printf("Message payload is not an Aptos consensus message: %T, returning NoOp\n", chosenMsg.Payload)
		return SchedulerDecision{
			DecisionType: NoOp,
		}
	}

	round := c.GetRound()
	timestamp := c.GetTimestamp()
	sender := ReplicaID(chosenMsg.Sender)
	receiver := ReplicaID(chosenMsg.Receiver)

	if s.networkFaults.IsNetworkFault(round, sender, receiver) {
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

		s.WriteMutation([]string{
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

func (s *ByzzFuzzScheduler) ApplyFault(f *faults.Fault) error {
	return nil
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
	s.networkFaults.Clear()
}
