package network

import (
	"fmt"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"golang.org/x/exp/rand"
)

type Mutator interface {
	Mutate(msg aptos.IConsensusMessage, seed int64) (string, error)
}

type AptosMutator struct {
	validators []aptos.AccountAddress
}

func NewAptosMutator(vs []aptos.AccountAddress) *AptosMutator {
	return &AptosMutator{
		validators: vs,
	}
}

// Mutate function applies a random mutation on the given original message based on the seed.
// It returns the name of the mutation applied and an error if the consensus msg type is not supported.
func (m *AptosMutator) Mutate(cMsg aptos.IConsensusMessage, seed int64) (string, error) {
	// Mutate payload in place
	mname := ""
	switch v := cMsg.(type) {
	case *aptos.ProposalMsg:
		mname = mutateProposalMsg(v, seed, m.validators)
	case *aptos.OptProposalMsg:
		mname = mutateOptProposalMsg(v, seed, m.validators)
	case *aptos.VoteMsg:
		mname = mutateVoteMsg(v, seed, m.validators)
	case *aptos.CommitMessage:
		mname = mutateCommitMessage(v, seed, m.validators)
	case *aptos.CommitVote:
		mname = mutateCommitVote(v, seed, m.validators)
	case *aptos.RoundTimeoutMsg:
		mname = mutateRoundTimeoutMsg(v, seed, m.validators)
	default:
		return "", fmt.Errorf("unsupported consensus message type: %T", cMsg)
	}

	return mname, nil
}

type mutation struct {
	name string
	fn   func()
}

func pickMutation(mutations []mutation, seed int64) string {
	rng := rand.New(rand.NewSource(uint64(seed)))
	chosen := mutations[rng.Intn(len(mutations))]
	chosen.fn()
	return chosen.name
}

func mutateProposalMsg(msg *aptos.ProposalMsg, seed int64, validators []aptos.AccountAddress) string {
	return pickMutation([]mutation{
		//Small Scope mutations
		{"proposal_round_increment", func() {
			// proposed.round > parent.round is a safety check, we need to increment both to avoid immediate rejection of the proposal
			msg.Proposal.BlockData.Round++
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Round++
		}},
		{"proposal_round_decrement", func() {
			if msg.Proposal.BlockData.Round > 1 && msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Round > 0 {
				msg.Proposal.BlockData.Round--
				msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Round--
			}
		}},
		{"proposal_epoch_increment", func() {
			msg.Proposal.BlockData.Epoch++
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Epoch++
		}},
		{"proposal_epoch_decrement", func() {
			if msg.Proposal.BlockData.Epoch > 0 && msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Epoch > 0 {
				msg.Proposal.BlockData.Epoch--
				msg.Proposal.BlockData.QuorumCert.VoteData.Parent.Epoch--
			}
		}},
		{"syncinfo_hqc_shift_rounds_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Round++
			vd.Proposed.Round++
		}},
		{"syncinfo_all_qcs_shift_up", func() {
			bump := func(vd *aptos.VoteData) {
				vd.Parent.Round++
				vd.Proposed.Round++
			}
			bump(&msg.SyncInfo.HighestQuorumCert.VoteData)
			if msg.SyncInfo.HighestOrderedCert.Some != nil {
				bump(&msg.SyncInfo.HighestOrderedCert.Some.VoteData)
			}
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 != nil {
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round++
			}
		}},
		{"syncinfo_hqc_downgrade_to_commit_block", func() {
			// point HighestQC at an older, real block
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			commit := &msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Proposed.ID = commit.ID
			vd.Proposed.Round = commit.Round
			vd.Proposed.Version = commit.Version
			// keep Parent as it is; it's the grandparent of commit, which also exists
		}},

		{"syncinfo_all_qcs_epoch_down", func() {
			vd1 := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd1.Parent.Epoch--
			vd1.Proposed.Epoch--
			if msg.SyncInfo.HighestOrderedCert.Some != nil {
				vd2 := &msg.SyncInfo.HighestOrderedCert.Some.VoteData
				vd2.Parent.Epoch--
				vd2.Proposed.Epoch--
			}
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 != nil {
				commit := &msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
				commit.Epoch--
			}
		}},

		// Structure Aware mutations
		{"proposal_timestamp_future", func() {
			msg.Proposal.BlockData.TimestampUsecs += 5_000_000
		}},
		{"proposal_timestamps_shift_past", func() {
			// Shift block and parent timestamps backward by the same amount to preserve
			// strict block.timestamp > parent.timestamp invariant, which is a safety check for accepting proposals
			const delta = uint64(1_000_000)
			parentTs := &msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.TimestampUsecs
			if msg.Proposal.BlockData.TimestampUsecs > delta && *parentTs > delta {
				msg.Proposal.BlockData.TimestampUsecs -= delta
				*parentTs -= delta
			}
		}},
		{"proposal_to_nilblock", func() {
			// Nil blocks require block.timestamp == parent.timestamp
			if msg.Proposal.BlockData.BlockType.Proposal != nil {
				msg.Proposal.BlockData.BlockType = aptos.BlockType{
					NilBlock: &aptos.BlockTypeNilBlock{
						FailedAuthors: msg.Proposal.BlockData.BlockType.Proposal.FailedAuthors,
					},
				}
				msg.Proposal.BlockData.TimestampUsecs =
					msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.TimestampUsecs
			}
		}},
		{"proposal_qc_swap_with_highest_qc", func() {
			// Replace the proposal's QC VoteData with SyncInfo's HighestQuorumCert VoteData
			msg.Proposal.BlockData.QuorumCert.VoteData = msg.SyncInfo.HighestQuorumCert.VoteData
		}},
	}, seed)
}

func mutateOptProposalMsg(msg *aptos.OptProposalMsg, seed int64, validators []aptos.AccountAddress) string {
	return pickMutation([]mutation{
		// Small Scope mutations
		// We need to preserve the safety check: optproposal.round > optproposal.parent.round
		// and optproposal.epoch == optproposal.parent.epoch, so we do joint shifts on both rounds and epochs
		{"optproposal_shift_rounds_up", func() {
			msg.BlockData.Round++
			msg.BlockData.Parent.Round++
		}},
		{"optproposal_shift_rounds_down", func() {
			if msg.BlockData.Round > 1 && msg.BlockData.Parent.Round > 0 {
				msg.BlockData.Round--
				msg.BlockData.Parent.Round--
			}
		}},
		{"optproposal_shift_epochs_up", func() {
			msg.BlockData.Epoch++
			msg.BlockData.Parent.Epoch++
		}},
		{"optproposal_shift_epochs_down", func() {
			if msg.BlockData.Epoch > 0 && msg.BlockData.Parent.Epoch > 0 {
				msg.BlockData.Epoch--
				msg.BlockData.Parent.Epoch--
			}
		}},
		{"optproposal_parent_version_shift_up", func() {
			msg.BlockData.Parent.Version++
		}},
		// Joint shifts on SyncInfo HighestQuorumCert VoteData (preserve Proposed.Round > Parent.Round)
		{"optproposal_syncinfo_hqc_shift_rounds_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Round++
			vd.Proposed.Round++
		}},
		{"optproposal_syncinfo_hqc_shift_rounds_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Round > 0 && vd.Proposed.Round > 0 {
				vd.Parent.Round--
				vd.Proposed.Round--
			}
		}},
		{"optproposal_syncinfo_hqc_shift_epochs_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Epoch++
			vd.Proposed.Epoch++
		}},
		{"optproposal_syncinfo_hqc_shift_epochs_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Epoch > 0 && vd.Proposed.Epoch > 0 {
				vd.Parent.Epoch--
				vd.Proposed.Epoch--
			}
		}},
		// Structure Aware mutations
		{"optproposal_parent_downgrade_to_commit_block", func() {
			// Point parent at the real committed block from SyncInfo (known to the receiver)
			// Realigns block.round to commit.Round+1 to preserve round == parent.round + 1
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			commit := msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			msg.BlockData.Parent.ID = commit.ID
			msg.BlockData.Parent.Round = commit.Round
			msg.BlockData.Parent.Epoch = commit.Epoch
			msg.BlockData.Parent.Version = commit.Version
			msg.BlockData.Round = commit.Round + 1
			msg.BlockData.Epoch = commit.Epoch
		}},
		{"optproposal_change_optblockbody_to_nil", func() {
			if msg.BlockData.BlockBody != nil {
				msg.BlockData.BlockBody = nil
			}
		}},
	}, seed)
}

func mutateVoteMsg(msg *aptos.VoteMsg, seed int64, validators []aptos.AccountAddress) string {
	return pickMutation([]mutation{
		// Small Scope mutations
		// Joint shifts on VoteData (preserve Proposed.Round > Parent.Round and Proposed.Epoch == Parent.Epoch)
		{"vote_shift_rounds_up", func() {
			msg.Vote.VoteData.Proposed.Round++
			msg.Vote.VoteData.Parent.Round++
		}},
		{"vote_shift_rounds_down", func() {
			if msg.Vote.VoteData.Proposed.Round > 1 && msg.Vote.VoteData.Parent.Round > 0 {
				msg.Vote.VoteData.Proposed.Round--
				msg.Vote.VoteData.Parent.Round--
			}
		}},
		{"vote_shift_epochs_up", func() {
			msg.Vote.VoteData.Proposed.Epoch++
			msg.Vote.VoteData.Parent.Epoch++
		}},
		{"vote_shift_epochs_down", func() {
			if msg.Vote.VoteData.Proposed.Epoch > 0 && msg.Vote.VoteData.Parent.Epoch > 0 {
				msg.Vote.VoteData.Proposed.Epoch--
				msg.Vote.VoteData.Parent.Epoch--
			}
		}},
		{"vote_parent_version_shift_up", func() {
			msg.Vote.VoteData.Parent.Version++
		}},
		// Joint shifts on SyncInfo HighestQuorumCert VoteData
		{"vote_syncinfo_hqc_shift_rounds_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Round++
			vd.Proposed.Round++
		}},
		{"vote_syncinfo_hqc_shift_rounds_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Round > 0 && vd.Proposed.Round > 0 {
				vd.Parent.Round--
				vd.Proposed.Round--
			}
		}},
		{"vote_syncinfo_hqc_shift_epochs_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Epoch++
			vd.Proposed.Epoch++
		}},
		{"vote_syncinfo_hqc_shift_epochs_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Epoch > 0 && vd.Proposed.Epoch > 0 {
				vd.Parent.Epoch--
				vd.Proposed.Epoch--
			}
		}},
		// Structure-aware
		{"vote_proposed_downgrade_to_commit_block", func() {
			// Point Proposed at the real committed block from SyncInfo (known to the receiver)
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			commit := &msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			vd := &msg.Vote.VoteData
			vd.Proposed.ID = commit.ID
			vd.Proposed.Round = commit.Round
			vd.Proposed.Epoch = commit.Epoch
			vd.Proposed.Version = commit.Version
			// ensure Parent.Round < Proposed.Round still holds
			if vd.Parent.Round >= vd.Proposed.Round {
				if vd.Proposed.Round > 0 {
					vd.Parent.Round = vd.Proposed.Round - 1
				}
			}
			vd.Parent.Epoch = commit.Epoch
		}},
		{"change_vote_to_appear_like_timeout", func() {
			msg.Vote.TwoChainTimeout = &aptos.OptionTwoChainTimeoutWithSig{Some: &aptos.TwoChainTimeoutWithSig{}}
		}},
		{"change_vote_author", func() {
			current := msg.Vote.Author
			others := make([]aptos.AccountAddress, 0)
			for _, v := range validators {
				if v != current {
					others = append(others, v)
				}
			}
			if len(others) > 0 {
				msg.Vote.Author = others[rand.Intn(len(others))]
			}
		}},
	}, seed)
}

func mutateCommitMessage(msg *aptos.CommitMessage, seed int64, validators []aptos.AccountAddress) string {
	// There is an exact-match on the embedded CommitInfo for Vote and Decision, so any
	// arithmetic on CommitInfo fields here is always rejected.
	return pickMutation([]mutation{
		// Structure Aware mutations
		{"commit_swap_ack_with_nack", func() {
			if msg.Ack != nil {
				msg.Ack = nil
				msg.Nack = &aptos.BcsUnit{}
			}
		}},
		{"commit_swap_nack_with_ack", func() {
			if msg.Nack != nil {
				msg.Nack = nil
				msg.Ack = &aptos.BcsUnit{}
			}
		}},
		{"commit_vote_change_author", func() {
			if msg.Vote == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			current := msg.Vote.Author
			others := make([]aptos.AccountAddress, 0)
			for _, v := range validators {
				if v != current {
					others = append(others, v)
				}
			}
			if len(others) > 0 {
				msg.Vote.Author = others[rng.Intn(len(others))]
			}
		}},
		{"commit_decision_to_vote_as_random_author", func() {
			// Convert a Decision into a CommitVote attributed to a random validator.
			// Decision->Vote keeps LedgerInfo intact (so buffer exact-match still passes);
			// the mutation is that a "decision" is now claimed as a single validator's vote.
			if msg.Decision == nil || msg.Decision.LedgerInfo.V0 == nil {
				return
			}
			if len(validators) == 0 {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			msg.Vote = &aptos.CommitVote{
				Author:     validators[rng.Intn(len(validators))],
				LedgerInfo: msg.Decision.LedgerInfo.V0.LedgerInfo,
			}
			msg.Decision = nil
		}},
	}, seed)
}

func mutateCommitVote(msg *aptos.CommitVote, seed int64, validators []aptos.AccountAddress) string {
	// There is an exact-match check on the entire CommitInfo, so any single-field
	// arithmetic mutation (Round/Epoch/Version/ID) causes "Inconsistent commit info" NACK. Only
	// fields outside CommitInfo (Author, ConsensusDataHash) can be mutated without resulting in an invalid mutation.
	return pickMutation([]mutation{
		// Structure Aware mutations
		{"commitvote_change_author", func() {
			current := msg.Author
			others := make([]aptos.AccountAddress, 0)
			for _, v := range validators {
				if v != current {
					others = append(others, v)
				}
			}
			if len(others) > 0 {
				msg.Author = others[rand.Intn(len(others))]
			}
		}},
		{"commitvote_change_consensusdatahash_to_random_value", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomHash aptos.HashValue
			for i := range randomHash {
				randomHash[i] = byte(rng.Intn(256))
			}
			msg.LedgerInfo.ConsensusDataHash = randomHash
		}},
	}, seed)
}

func mutateRoundTimeoutMsg(msg *aptos.RoundTimeoutMsg, seed int64, validators []aptos.AccountAddress) string {
	rng := rand.New(rand.NewSource(uint64(seed)))
	return pickMutation([]mutation{
		// Invariants: Timeout.Epoch == QC.Proposed.Epoch (all same epoch) and
		// Timeout.Round > QC.certified_block.Round (timing out a round after the QC)
		// Round++ alone keeps Round > QC.Round; Round-- must shift QC rounds too
		// Epoch changes must shift Timeout.Epoch and all embedded QC epochs together
		{"roundtimeout_round_shift_up", func() {
			msg.RoundTimeout.Timeout.Round++
		}},
		{"roundtimeout_round_shift_down", func() {
			qc := &msg.RoundTimeout.Timeout.QuorumCert.VoteData
			if msg.RoundTimeout.Timeout.Round > 1 &&
				qc.Proposed.Round > 0 && qc.Parent.Round > 0 {
				msg.RoundTimeout.Timeout.Round--
				qc.Proposed.Round--
				qc.Parent.Round--
			}
		}},
		{"roundtimeout_epoch_shift_up", func() {
			msg.RoundTimeout.Timeout.Epoch++
			qc := &msg.RoundTimeout.Timeout.QuorumCert.VoteData
			qc.Parent.Epoch++
			qc.Proposed.Epoch++
		}},
		{"roundtimeout_epoch_shift_down", func() {
			qc := &msg.RoundTimeout.Timeout.QuorumCert.VoteData
			if msg.RoundTimeout.Timeout.Epoch > 0 &&
				qc.Parent.Epoch > 0 && qc.Proposed.Epoch > 0 {
				msg.RoundTimeout.Timeout.Epoch--
				qc.Parent.Epoch--
				qc.Proposed.Epoch--
			}
		}},
		// Joint shifts on SyncInfo HighestQuorumCert VoteData
		{"roundtimeout_syncinfo_hqc_shift_rounds_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Round++
			vd.Proposed.Round++
		}},
		{"roundtimeout_syncinfo_hqc_shift_rounds_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Round > 0 && vd.Proposed.Round > 0 {
				vd.Parent.Round--
				vd.Proposed.Round--
			}
		}},
		{"roundtimeout_syncinfo_hqc_shift_epochs_up", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			vd.Parent.Epoch++
			vd.Proposed.Epoch++
		}},
		{"roundtimeout_syncinfo_hqc_shift_epochs_down", func() {
			vd := &msg.SyncInfo.HighestQuorumCert.VoteData
			if vd.Parent.Epoch > 0 && vd.Proposed.Epoch > 0 {
				vd.Parent.Epoch--
				vd.Proposed.Epoch--
			}
		}},
		// Structure-aware
		{"roundtimeout_change_author", func() {
			current := msg.RoundTimeout.Author
			others := make([]aptos.AccountAddress, 0)
			for _, v := range validators {
				if v != current {
					others = append(others, v)
				}
			}
			if len(others) > 0 {
				msg.RoundTimeout.Author = others[rng.Intn(len(others))]
			}
		}},
		{"roundtimeout_change_reason", func() {
			current := msg.RoundTimeout.Reason

			numValidators := len(validators)
			numBytes := (numValidators + 7) / 8
			bitmask := make([]byte, numBytes)
			for i := 0; i < numValidators; i++ {
				// for each validator, randomly decide if it's missing or not
				if rng.Intn(2) == 1 {
					bitmask[i/8] |= 1 << (7 - uint(i%8))
				}
			}

			reasons := []aptos.RoundTimeoutReason{
				{Unknown: &aptos.BcsUnit{}},
				{ProposalNotReceived: &aptos.BcsUnit{}},
				{NoQC: &aptos.BcsUnit{}},
				{PayloadUnavailable: &aptos.RoundTimeoutReasonPayloadUnavailable{
					MissingAuthors: aptos.BitVec{Inner: bitmask},
				}},
			}

			var others []aptos.RoundTimeoutReason
			for _, r := range reasons {
				if !sameReason(r, current) {
					others = append(others, r)
				}
			}
			if len(others) > 0 {
				msg.RoundTimeout.Reason = others[rng.Intn(len(others))]
			}
		}},
	}, seed)
}

// Helpers
func sameReason(a, b aptos.RoundTimeoutReason) bool {
	return (a.Unknown != nil && b.Unknown != nil) ||
		(a.ProposalNotReceived != nil && b.ProposalNotReceived != nil) ||
		(a.NoQC != nil && b.NoQC != nil) ||
		(a.PayloadUnavailable != nil && b.PayloadUnavailable != nil)
}
