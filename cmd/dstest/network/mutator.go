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

func (m *AptosMutator) Mutate(ogMsg *Message, seed int64) error {
	consensusMsg, ok := ogMsg.Payload.(aptos.IConsensusMessage)
	if !ok {
		return fmt.Errorf("payload is not an Aptos consensus message: %T", ogMsg.Payload)
	}

	var mutated aptos.IConsensusMessage
	switch v := consensusMsg.(type) {
	case aptos.ProposalMsg:
		mutated = mutateProposalMsg(v, seed)
	case aptos.OptProposalMsg:
		mutated = mutateOptProposalMsg(v, seed)
	case aptos.VoteMsg:
		mutated = mutateVoteMsg(v, seed)
	case aptos.CommitMessage:
		mutated = mutateCommitMessage(v, seed)
	case aptos.CommitVote:
		mutated = mutateCommitVote(v, seed)
	case aptos.RoundTimeoutMsg:
		mutated = mutateRoundTimeoutMsg(v, seed)
	default:
		return fmt.Errorf("unsupported consensus payload type: %T", consensusMsg)
	}

	ogMsg.Payload = mutated
	return nil
}

func mutateProposalMsg(msg aptos.ProposalMsg, seed int64) aptos.ProposalMsg {
	// based on seed, increment or decrement the round number by 1
	rng := rand.New(rand.NewSource(uint64(seed)))

	m := msg

	if rng.Intn(2) == 0 {
		m.Proposal.BlockData.Round++
	} else if m.Proposal.BlockData.Round > 0 {
		m.Proposal.BlockData.Round--
	}

	return m
}

func mutateOptProposalMsg(msg aptos.OptProposalMsg, seed int64) aptos.OptProposalMsg {
	return msg
}

func mutateVoteMsg(msg aptos.VoteMsg, seed int64) aptos.VoteMsg {
	return msg
}

func mutateCommitMessage(msg aptos.CommitMessage, seed int64) aptos.CommitMessage {
	return msg
}

func mutateCommitVote(msg aptos.CommitVote, seed int64) aptos.CommitVote {
	return msg
}

func mutateRoundTimeoutMsg(msg aptos.RoundTimeoutMsg, seed int64) aptos.RoundTimeoutMsg {
	return msg
}
