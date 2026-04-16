package network

import (
	"fmt"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"golang.org/x/exp/rand"
)

type Mutator interface {
	Mutate(msg *Message, seed int64) error
}

type AptosMutator struct{}

func NewAptosMutator() *AptosMutator {
	return &AptosMutator{}
}

func (m *AptosMutator) Mutate(ogMsg *Message, seed int64) error {
	consensusMsg, ok := ogMsg.Payload.(aptos.IConsensusMessage)
	if !ok {
		return fmt.Errorf("payload is not an Aptos consensus message: %T", ogMsg.Payload)
	}

	// Mutate payload in place
	switch v := consensusMsg.(type) {
	case *aptos.ProposalMsg:
		mutateProposalMsg(v, seed)
	case *aptos.OptProposalMsg:
		mutateOptProposalMsg(v, seed)
	case *aptos.VoteMsg:
		mutateVoteMsg(v, seed)
	case *aptos.CommitMessage:
		mutateCommitMessage(v, seed)
	case *aptos.CommitVote:
		mutateCommitVote(v, seed)
	case *aptos.RoundTimeoutMsg:
		mutateRoundTimeoutMsg(v, seed)
	default:
		return fmt.Errorf("unsupported consensus payload type: %T", consensusMsg)
	}

	return nil
}

func mutateProposalMsg(msg *aptos.ProposalMsg, seed int64) {
	// based on seed, increment or decrement the round number by 1
	rng := rand.New(rand.NewSource(uint64(seed)))

	if rng.Intn(2) == 0 {
		msg.Proposal.BlockData.Round++
	} else if msg.Proposal.BlockData.Round > 0 {
		msg.Proposal.BlockData.Round--
	}
}

func mutateOptProposalMsg(msg *aptos.OptProposalMsg, seed int64) {

}

func mutateVoteMsg(msg *aptos.VoteMsg, seed int64) {

}

func mutateCommitMessage(msg *aptos.CommitMessage, seed int64) {
}

func mutateCommitVote(msg *aptos.CommitVote, seed int64) {
}

func mutateRoundTimeoutMsg(msg *aptos.RoundTimeoutMsg, seed int64) {
	rng := rand.New(rand.NewSource(uint64(seed)))

	if rng.Intn(2) == 0 {
		msg.RoundTimeout.Timeout.Round++
	}
}
