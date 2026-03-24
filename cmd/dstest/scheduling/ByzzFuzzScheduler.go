package scheduling

import (
	"github.com/egeberkaygulcan/dstest/cmd/dstest/config"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/faults"
	"github.com/egeberkaygulcan/dstest/cmd/dstest/network"
)

type ByzzFuzzScheduler struct {
	Scheduler
	Config         *config.Config
	NetworkManager *network.Manager
}

// assert ByzzFuzz implements the Scheduler interface
var _ Scheduler = &ByzzFuzzScheduler{}

func (s *ByzzFuzzScheduler) Init(config *config.Config) {
	s.Config = config
}

func (s *ByzzFuzzScheduler) NextIteration() {}

func (s *ByzzFuzzScheduler) Reset() {}

func (s *ByzzFuzzScheduler) Shutdown() {}

func (s *ByzzFuzzScheduler) Next(messages []*network.Message, faults []*faults.Fault, context faults.FaultContext) SchedulerDecision {
	return SchedulerDecision{
		DecisionType: NoOp,
	}
}

func (s *ByzzFuzzScheduler) GetClientRequest() int {
	return -1
}
