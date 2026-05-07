package network

import (
	"fmt"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"golang.org/x/exp/rand"
)

type Mutator interface {
	Mutate(msg aptos.IConsensusMessage, seed int64) (string, error)
}

//   - keysByAuthor maps validator addresses to their consensus private keys,
//     needed for resigning msgs after mutations
//   - orderedAddrs are the validator addresses in genesis registration order
//     (v0, v1, v2...); used for QC bitmask mutations where we need to know the order of validators
//     to produce valid (aggregated) signatures
type AptosMutator struct {
	keysByAuthor map[aptos.AccountAddress]*aptos.SK
	orderedAddrs []aptos.AccountAddress
}

func NewAptosMutator(keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) *AptosMutator {
	return &AptosMutator{
		keysByAuthor: keysByAuthor,
		orderedAddrs: orderedAddrs,
	}
}

// Mutate function applies a random mutation on the given original message based on the seed.
// It returns the name of the mutation applied and an error if the consensus msg type is not supported.
// The payload is mutated in place.
func (m *AptosMutator) Mutate(cMsg aptos.IConsensusMessage, seed int64) (string, error) {
	mname := ""
	switch v := cMsg.(type) {
	case *aptos.ProposalMsg:
		mname = mutateProposalMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.OptProposalMsg:
		mname = mutateOptProposalMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.VoteMsg:
		mname = mutateVoteMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.CommitMessage:
		mname = mutateCommitMessage(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.CommitVote:
		mname = mutateCommitVote(v, seed, m.keysByAuthor)
	case *aptos.RoundTimeoutMsg:
		mname = mutateRoundTimeoutMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
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

// syncInfoMutations returns mutations that operate on a SyncInfo
// Reused by every message type that carries a SyncInfo (ProposalMsg, OptProposalMsg,
// VoteMsg, RoundTimeoutMsg);
// Mutations that change a QC/WrappedLedgerInfo VoteData also re-sign
// the QC/WrappedLedgerInfo aggregate signature (via ResignAggregate / ResignQC / ResignWrappedLedgerInfo)
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - Per QC (in every VoteData): Proposed.Round > Parent.Round, Proposed.Epoch == Parent.Epoch,
//     Parent.Version <= Proposed.Version, Parent.Timestamp <= Proposed.Timestamp
//   - HQC.round >= HOC.round >= HCC.round
//   - HQC, HOC, HCC must be in the same epoch
func syncInfoMutations(
	si *aptos.SyncInfo,
	keysByAuthor map[aptos.AccountAddress]*aptos.SK,
	orderedAddrs []aptos.AccountAddress,
) []mutation {
	bumpRounds := func(vd *aptos.VoteData, li *aptos.LedgerInfoWithSignatures) {
		vd.Parent.Round++
		vd.Proposed.Round++
		_ = aptos.ResignAggregate(*vd, li, keysByAuthor, orderedAddrs)
	}
	dropRounds := func(vd *aptos.VoteData, li *aptos.LedgerInfoWithSignatures) {
		if vd.Parent.Round == 0 || vd.Proposed.Round == 0 {
			return
		}
		vd.Parent.Round--
		vd.Proposed.Round--
		_ = aptos.ResignAggregate(*vd, li, keysByAuthor, orderedAddrs)
	}
	bumpEpochs := func(vd *aptos.VoteData, li *aptos.LedgerInfoWithSignatures) {
		vd.Parent.Epoch++
		vd.Proposed.Epoch++
		_ = aptos.ResignAggregate(*vd, li, keysByAuthor, orderedAddrs)
	}
	dropEpochs := func(vd *aptos.VoteData, li *aptos.LedgerInfoWithSignatures) {
		if vd.Parent.Epoch == 0 || vd.Proposed.Epoch == 0 {
			return
		}
		vd.Parent.Epoch--
		vd.Proposed.Epoch--
		_ = aptos.ResignAggregate(*vd, li, keysByAuthor, orderedAddrs)
	}

	return []mutation{
		//Small-scope mutations
		{"syncinfo_hqc_shift_rounds_up", func() {
			bumpRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
		}},
		{"syncinfo_hqc_shift_rounds_down", func() {
			dropRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_rounds_up", func() {
			bumpRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				bumpRounds(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			bumpRounds(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_rounds_down", func() {
			dropRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				dropRounds(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			dropRounds(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_epochs_up", func() {
			bumpEpochs(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				bumpEpochs(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			bumpEpochs(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_epochs_down", func() {
			dropEpochs(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				dropEpochs(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			dropEpochs(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_hqc_timestamp_increase", func() {
			si.HighestQuorumCert.VoteData.Proposed.TimestampUsecs += 5_000_000
			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
		//Structure aware mutations
		{"syncinfo_all_qcs_downgrade_to_commit_block", func() {
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			commit := si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo

			// Point HQC, HOC, and HCC's certified-block all at the committed block
			apply := func(vd *aptos.VoteData) {
				vd.Proposed.ID = commit.ID
				vd.Proposed.Round = commit.Round
				vd.Proposed.Epoch = commit.Epoch
				vd.Proposed.Version = commit.Version
				// Maintain Proposed.Round > Parent.Round invariant
				if vd.Parent.Round >= vd.Proposed.Round {
					if vd.Proposed.Round == 0 {
						return
					}
					vd.Parent.Round = vd.Proposed.Round - 1
				}
			}

			apply(&si.HighestQuorumCert.VoteData)
			if si.HighestOrderedCert.Some != nil {
				apply(&si.HighestOrderedCert.Some.VoteData)
			}
			apply(&si.HighestCommitCert.VoteData)

			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
			if si.HighestOrderedCert.Some != nil {
				_ = aptos.ResignWrappedLedgerInfo(si.HighestOrderedCert.Some, keysByAuthor, orderedAddrs)
			}
			_ = aptos.ResignWrappedLedgerInfo(&si.HighestCommitCert, keysByAuthor, orderedAddrs)
		}},
		{"syncinfo_hqc_align_with_hoc", func() {
			if si.HighestOrderedCert.Some == nil {
				return
			}
			// Copy both VoteData and SignedLedgerInfo together; preserves HOC's aggregate
			// sig over HOC's LedgerInfo, which is still valid after the copy; No re-sign needed
			si.HighestQuorumCert.VoteData = si.HighestOrderedCert.Some.VoteData
			si.HighestQuorumCert.SignedLedgerInfo = si.HighestOrderedCert.Some.SignedLedgerInfo
		}},
		{"syncinfo_hoc_align_with_hqc", func() {
			if si.HighestOrderedCert.Some == nil {
				si.HighestOrderedCert.Some = &aptos.WrappedLedgerInfo{}
			}
			si.HighestOrderedCert.Some.VoteData = si.HighestQuorumCert.VoteData
			si.HighestOrderedCert.Some.SignedLedgerInfo = si.HighestQuorumCert.SignedLedgerInfo
		}},
		{"syncinfo_hcc_align_with_hoc", func() {
			if si.HighestOrderedCert.Some == nil {
				return
			}
			si.HighestCommitCert = *si.HighestOrderedCert.Some
		}},
		{"syncinfo_qc_parent_swap_with_commit_id", func() {
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			commit := si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			vd := &si.HighestQuorumCert.VoteData
			// Parent.Round must stay < Proposed.Round
			if commit.Round >= vd.Proposed.Round {
				return
			}
			// Parent.Epoch must equal Proposed.Epoch
			if commit.Epoch != vd.Proposed.Epoch {
				return
			}
			// Parent.Version must be <= Proposed.Version
			if commit.Version > vd.Proposed.Version {
				return
			}

			vd.Parent.ID = commit.ID
			vd.Parent.Round = commit.Round
			vd.Parent.Version = commit.Version
			// we keep Parent.Epoch, TimestampUsecs consistent with Proposed

			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"syncinfo_qc_executed_state_swap", func() {
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			si.HighestQuorumCert.VoteData.Proposed.ExecutedStateID =
				si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
	}
}

// ProposalMsg need resigning with the proposer's SK
// if the mutation touches any field that is part of the BlockData struct (which is the signed payload)
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - Parent.Round < BlockProposed.Round, where parent of this proposed block is QC.VoteData.Proposed
//   - Parent.Epoch == BlockProposed.Epoch == SyncInfo.HQC.Proposed.Epoch
//   - Parent.ID == SyncInfo.HQC.Proposed.ID
//   - BlockProposed.Round - 1 == max(Parent.Round, SyncInfo.H2CTC.Timeout.Round (if it exists))
//   - BlockProposed.Timestamp_usecs > Parent.Timestamp_usecs; for nil/reconfig blocks they should be equal
//   - BlockProposed.Timestamp_usecs <= now + 5min (for non-nil,non-reconfig blocks)
//   - BlockProposed.Author == sender
//   - BlockProposed.Epoch == receiver_local_epoch
func mutateProposalMsg(msg *aptos.ProposalMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) string {
	resign := func() {
		if msg.Proposal.BlockData.BlockType.Proposal == nil {
			return // unsigned variant (NilBlock, Genesis), no proposer to sign with
		}
		sk := keysByAuthor[msg.Proposal.BlockData.BlockType.Proposal.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignProposal(&msg.Proposal, sk)
	}

	mutations := []mutation{
		// Small Scope mutations
		{"proposal_large_timestamp_future", func() {
			msg.Proposal.BlockData.TimestampUsecs += 5_000_000
			resign()
		}},
		{"proposal_short_timestamp_future", func() {
			msg.Proposal.BlockData.TimestampUsecs += 500_000
			resign()
		}},
		{"proposal_timestamps_shift_past", func() {
			const delta = uint64(1_000_000)
			parentTs := &msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.TimestampUsecs
			grandpTs := &msg.Proposal.BlockData.QuorumCert.VoteData.Parent.TimestampUsecs

			if msg.Proposal.BlockData.TimestampUsecs > delta && *parentTs > delta && *grandpTs > delta {
				msg.Proposal.BlockData.TimestampUsecs -= delta
				*parentTs -= delta
				*grandpTs -= delta
				aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
				resign()
			}
		}},
		// Structure-aware mutations
		{"proposal_qc_votedata_swap_with_syncinfo_highest_qc_votedata", func() {
			newVD := msg.SyncInfo.HighestQuorumCert.VoteData
			block := &msg.Proposal.BlockData

			if newVD.Proposed.Round >= block.Round {
				return
			}
			if newVD.Proposed.Epoch != block.Epoch {
				return
			}
			if newVD.Proposed.TimestampUsecs >= block.TimestampUsecs {
				return
			}
			if newVD.Proposed.Round != block.Round-1 {
				return
			}
			if newVD.Parent.Version > newVD.Proposed.Version {
				return
			}
			block.QuorumCert.VoteData = newVD
			aptos.ResignQC(&block.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_to_optimistic_proposal", func() {
			if msg.Proposal.BlockData.BlockType.Proposal == nil {
				return
			}
			p := msg.Proposal.BlockData.BlockType.Proposal
			msg.Proposal.BlockData.BlockType = aptos.BlockType{
				OptimisticProposal: &aptos.OptBlockBody{
					V0: &aptos.OptBlockBodyV0{
						Payload:       p.Payload,
						Author:        p.Author,
						GrandparentQC: msg.Proposal.BlockData.QuorumCert,
					},
				},
			}
			// we resign, although we expect the proposer signature to be ignored on OptProposal block types
			resign()
		}},
		{"proposal_parent_swap_executed_state_with_hcc", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_inject_next_epoch_state", func() {
			if msg.Proposal.BlockData.BlockType.Proposal == nil {
				return
			}
			parent := &msg.Proposal.BlockData.QuorumCert.VoteData.Proposed
			parent.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    parent.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_grandparent_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ID = randomID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ID = randomID
			msg.SyncInfo.HighestQuorumCert.VoteData.Proposed.ID = randomID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			aptos.ResignQC(&msg.SyncInfo.HighestQuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_payload_empty", func() {
			if msg.Proposal.BlockData.BlockType.Proposal == nil {
				return
			}
			p := &msg.Proposal.BlockData.BlockType.Proposal.Payload
			switch {
			case p.QuorumStoreInlineHybridV2 != nil:
				p.QuorumStoreInlineHybridV2 = &aptos.QuorumStoreInlineHybridV2{}
			case p.QuorumStoreInlineHybrid != nil:
				p.QuorumStoreInlineHybrid = &aptos.QuorumStoreInlineHybrid{}
			case p.DirectMempool != nil:
				empty := []aptos.SignedTransaction{}
				p.DirectMempool = &empty
			default:
				return
			}
			resign()
		}},
	}

	mutations = append(mutations, syncInfoMutations(&msg.SyncInfo, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

// OptProposalMsg carries no proposer signature; the message is authenticated through
// the grandparent QC's aggregate signature. Mutations that touch the grandparent QC's
// (OptBlockData.BlockBody.V0.GrandparentQC) VoteData require ResignQC;
// Mutations on plain fields from OptBlockData (like Parent or Timestamp) require no resigning
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - BlockProposed.Epoch == receiver_local_epoch
//   - BlockProposed.Epoch == Parent.Epoch == GrandparentQC.VoteData.Proposed.Epoch == SyncInfo.HQC.VoteData.Proposed.Epoch
//   - BlockProposed.Round > 1
//   - Strict +1 chain: BlockProposed.Round == Parent.Round + 1 == GrandparentQC.VoteData.Proposed.Round + 2
//   - GrandparentQC.VoteData.Proposed.ID == SyncInfo.HQC.VoteData.Proposed.ID
//   - !GrandparentQC.VoteData.Proposed.has_reconfiguration() (opt proposals are disallowed after a reconfig event
//     which is the event in which the epoch changes)
//   - SyncInfo.H2CTC == None (opt proposals can't carry a timeout cert)
//   - BlockProposed.TimestampUsecs > Parent.TimestampUsecs > GrandparentQC.VoteData.Proposed.TimestampUsecs
//   - BlockProposed.TimestampUsecs <= now + 5min
//   - local_hqc.Round + 1 == BlockData.Round
//     and local_hqc.ID == BlockData.Parent.ID (receiver's local stored hqc)
func mutateOptProposalMsg(msg *aptos.OptProposalMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) string {
	mutations := []mutation{
		// Small Scope mutations
		{"optproposal_large_timestamp_future", func() {
			msg.BlockData.TimestampUsecs += 5_000_000
		}},
		{"optproposal_short_timestamp_future", func() {
			msg.BlockData.TimestampUsecs += 500_000
		}},
		{"optproposal_timestamps_shift_past", func() {
			// Shift block, parent, and grandparent_qc.cert/parent timestamps back by 1s
			// equally so the strict ordering (block > parent > grandparent) is preserved
			// Within grandparent_qc it is also required that proposed.ts >= parent.ts
			const delta = uint64(1_000_000)
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			gpVD := &msg.BlockData.BlockBody.V0.GrandparentQC.VoteData
			selfTs := &msg.BlockData.TimestampUsecs
			parentTs := &msg.BlockData.Parent.TimestampUsecs
			gpProposedTs := &gpVD.Proposed.TimestampUsecs
			gpParentTs := &gpVD.Parent.TimestampUsecs
			if *selfTs > delta && *parentTs > delta && *gpProposedTs > delta && *gpParentTs > delta {
				*selfTs -= delta
				*parentTs -= delta
				*gpProposedTs -= delta
				*gpParentTs -= delta
				_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
			}
		}},
		{"optproposal_parent_version_shift_up", func() {
			msg.BlockData.Parent.Version++
		}},
		// Structure-aware mutations
		{"optproposal_parent_executed_state_swap_with_syncinfo_hcc_executed_state", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.BlockData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
		}},
		{"optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hcc_executed_state", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_greatgrandparent_id_swap", func() {
			// Mutate GrandParentQC.VoteData.Parent.ID (the great-grandparent of the opt
			// block)
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Parent.ID = randomID
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_grandparent_qc_proposed_id_swap", func() {
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ID = randomID
			msg.SyncInfo.HighestQuorumCert.VoteData.Proposed.ID = randomID
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
			_ = aptos.ResignQC(&msg.SyncInfo.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_payload_empty", func() {
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			p := &msg.BlockData.BlockBody.V0.Payload
			switch {
			case p.QuorumStoreInlineHybridV2 != nil:
				p.QuorumStoreInlineHybridV2 = &aptos.QuorumStoreInlineHybridV2{}
			case p.QuorumStoreInlineHybrid != nil:
				p.QuorumStoreInlineHybrid = &aptos.QuorumStoreInlineHybrid{}
			case p.DirectMempool != nil:
				empty := []aptos.SignedTransaction{}
				p.DirectMempool = &empty
			default:
				return
			}
		}},
	}

	mutations = append(mutations, syncInfoMutations(&msg.SyncInfo, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

// VoteMsg need resigning with the voter's SK
// if the mutation touches any field that is part of the VoteData or LedgerInfo structs.
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - VoteData.Proposed.Epoch == receiver_local_epoch
//   - VoteData.Proposed.Epoch == SyncInfo.HQC.Cert.Epoch (where .Cert == VoteData.Proposed)
//   - VoteData.Proposed.Round > SyncInfo.HighestRound (= max(HQC.Cert.Round, highest_timeout_round))
//   - Vote.Author == network sender
//   - In VoteData: Parent.Epoch == Proposed.Epoch; Parent.Round < Proposed.Round; Parent.Ts <= Proposed.Ts;
//     Proposed.Version == 0 or Parent.Version <= Proposed.Version
//   - LedgerInfo.ConsensusDataHash == VoteData.hash()
//   - If TwoChainTimeout.Some, Timeout.Qc.HQC.Cert.Round <= SyncInfo.HQC.Cert.Round
//   - VoteData.Proposed.Round == receiver_local_round (after sync_up)
//   - Receiver must be the leader for VoteData.Proposed.Round + 1 to process the incoming vote
func mutateVoteMsg(msg *aptos.VoteMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) string {
	resign := func() {
		sk := keysByAuthor[msg.Vote.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignVote(&msg.Vote, sk)
	}
	mutations := []mutation{
		// Small Scope mutations
		{"vote_parent_version_shift_up", func() {
			msg.Vote.VoteData.Parent.Version++
			resign()
		}},
		{"vote_proposed_timestamp_large_future", func() {
			msg.Vote.VoteData.Proposed.TimestampUsecs += 5_000_000
			resign()
		}},
		{"vote_proposed_timestamp_short_future", func() {
			msg.Vote.VoteData.Proposed.TimestampUsecs += 500_000
			resign()
		}},
		{"vote_timestamps_shift_past", func() {
			// Shift proposed and parent timestamps back by 1s equally so parent.ts <= proposed.ts holds
			const delta = uint64(1_000_000)
			proposedTs := &msg.Vote.VoteData.Proposed.TimestampUsecs
			parentTs := &msg.Vote.VoteData.Parent.TimestampUsecs
			if *proposedTs > delta && *parentTs > delta {
				*proposedTs -= delta
				*parentTs -= delta
				resign()
			}
		}},
		{"vote_parent_round_shift_down", func() {
			if msg.Vote.VoteData.Parent.Round == 0 {
				return
			}
			msg.Vote.VoteData.Parent.Round--
			resign()
		}},
		// Structure-aware mutations
		{"vote_proposed_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Vote.VoteData.Proposed.ID = randomID
			resign()
		}},
		{"vote_parent_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Vote.VoteData.Parent.ID = randomID
			resign()
		}},
		{"vote_proposed_executed_state_swap_with_syncinfo_hcc_executed_state", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_parent_executed_state_swap_with_syncinfo_hcc_executed_state", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_proposed_inject_next_epoch_state", func() {
			proposed := &msg.Vote.VoteData.Proposed
			proposed.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    proposed.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"vote_parent_inject_next_epoch_state", func() {
			parent := &msg.Vote.VoteData.Parent
			parent.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    parent.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"vote_ledger_info_commit_info_swap_with_syncinfo_hcc", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.LedgerInfo.CommitInfo = msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			resign()
		}},
		{"attach_timeout_to_vote", func() {
			// Upgrade a regular vote into a timeout vote by attaching a 2-chain timeout signed by the
			// voter
			epoch := msg.Vote.VoteData.Proposed.Epoch
			round := msg.Vote.VoteData.Proposed.Round
			hqcRound := msg.SyncInfo.HighestQuorumCert.VoteData.Proposed.Round

			sk := keysByAuthor[msg.Vote.Author]
			if sk == nil {
				return
			}

			sig, err := aptos.SignTimeoutRepr(sk, epoch, round, hqcRound)
			if err != nil {
				return
			}

			msg.Vote.TwoChainTimeout = &aptos.OptionTwoChainTimeoutWithSig{
				Some: &aptos.TwoChainTimeoutWithSig{
					Timeout: aptos.TwoChainTimeout{
						Epoch:      epoch,
						Round:      round,
						QuorumCert: msg.SyncInfo.HighestQuorumCert,
					},
					Signature: sig,
				},
			}
		}},
		{"attach_timeout_to_vote_with_hcc_qc", func() {
			// Variant of attach_timeout_to_vote that embeds sync_info.HCC's certificate instead of HQC
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			epoch := msg.Vote.VoteData.Proposed.Epoch
			round := msg.Vote.VoteData.Proposed.Round
			hccRound := msg.SyncInfo.HighestCommitCert.VoteData.Proposed.Round

			sk := keysByAuthor[msg.Vote.Author]
			if sk == nil {
				return
			}

			sig, err := aptos.SignTimeoutRepr(sk, epoch, round, hccRound)
			if err != nil {
				return
			}

			msg.Vote.TwoChainTimeout = &aptos.OptionTwoChainTimeoutWithSig{
				Some: &aptos.TwoChainTimeoutWithSig{
					Timeout: aptos.TwoChainTimeout{
						Epoch: epoch,
						Round: round,
						QuorumCert: aptos.QuorumCert{
							VoteData:         msg.SyncInfo.HighestCommitCert.VoteData,
							SignedLedgerInfo: msg.SyncInfo.HighestCommitCert.SignedLedgerInfo,
						},
					},
					Signature: sig,
				},
			}
		}},
		{"attach_timeout_to_vote_with_hoc_qc", func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil {
				return
			}
			hoc := msg.SyncInfo.HighestOrderedCert.Some
			epoch := msg.Vote.VoteData.Proposed.Epoch
			round := msg.Vote.VoteData.Proposed.Round
			hocRound := hoc.VoteData.Proposed.Round

			sk := keysByAuthor[msg.Vote.Author]
			if sk == nil {
				return
			}

			sig, err := aptos.SignTimeoutRepr(sk, epoch, round, hocRound)
			if err != nil {
				return
			}

			msg.Vote.TwoChainTimeout = &aptos.OptionTwoChainTimeoutWithSig{
				Some: &aptos.TwoChainTimeoutWithSig{
					Timeout: aptos.TwoChainTimeout{
						Epoch: epoch,
						Round: round,
						QuorumCert: aptos.QuorumCert{
							VoteData:         hoc.VoteData,
							SignedLedgerInfo: hoc.SignedLedgerInfo,
						},
					},
					Signature: sig,
				},
			}
		}},
	}

	mutations = append(mutations, syncInfoMutations(&msg.SyncInfo, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

// CommitMessage is an enum with 4 variants:
//   - Vote(CommitVote): voter signs LedgerInfo; mutations on the LedgerInfo require ResignCommitVote
//   - Decision(CommitDecision): aggregate sig over LedgerInfo; mutations on the LedgerInfo
//     require ResignCommitDecision (re-aggregates with the same bitmask)
//   - Ack(()) / Nack: response-direction unit messages; no signatures
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - For incoming Vote/Decision: CommitMessage.Epoch == receiver_local_epoch
//   - For Vote: Vote.Author == network sender
func mutateCommitMessage(msg *aptos.CommitMessage, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) string {
	resign := func() {
		if msg.Vote != nil {
			sk := keysByAuthor[msg.Vote.Author]
			if sk != nil {
				_ = aptos.ResignCommitVote(msg.Vote, sk)
			}
		}
		if msg.Decision != nil {
			_ = aptos.ResignCommitDecision(msg.Decision, keysByAuthor, orderedAddrs)
		}
	}
	return pickMutation([]mutation{
		// Structure-aware mutations
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
		{"commit_vote_alter_consensus_data_hash", func() {
			if msg.Vote == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomHash aptos.HashValue
			for i := range randomHash {
				randomHash[i] = byte(rng.Intn(256))
			}
			msg.Vote.LedgerInfo.ConsensusDataHash = randomHash
			resign()
		}},
		{"commit_vote_alter_commit_info_id", func() {
			if msg.Vote == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Vote.LedgerInfo.CommitInfo.ID = randomID
			resign()
		}},
		{"commit_decision_alter_consensus_data_hash", func() {
			if msg.Decision == nil || msg.Decision.LedgerInfo.V0 == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomHash aptos.HashValue
			for i := range randomHash {
				randomHash[i] = byte(rng.Intn(256))
			}
			msg.Decision.LedgerInfo.V0.LedgerInfo.ConsensusDataHash = randomHash
			resign()
		}},
		{"commit_decision_alter_commit_info_id", func() {
			if msg.Decision == nil || msg.Decision.LedgerInfo.V0 == nil {
				return
			}
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.Decision.LedgerInfo.V0.LedgerInfo.CommitInfo.ID = randomID
			resign()
		}},
		{"commit_vote_to_decision_with_full_quorum", func() {
			if msg.Vote == nil {
				return
			}
			lis, err := aptos.BuildFullQuorumLedgerInfoWithSignatures(
				msg.Vote.LedgerInfo, keysByAuthor, orderedAddrs)
			if err != nil {
				return
			}
			msg.Decision = &aptos.CommitDecision{LedgerInfo: lis}
			msg.Vote = nil
		}},
	}, seed)
}

// CommitVote need resigning with the voter's SK
// if we mutate any field that is part of the LedgerInfo struct (which is the signed payload)
func mutateCommitVote(msg *aptos.CommitVote, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK) string {
	resign := func() {
		sk := keysByAuthor[msg.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignCommitVote(msg, sk)
	}
	return pickMutation([]mutation{
		{"commit_vote_alter_commit_info_id", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.LedgerInfo.CommitInfo.ID = randomID
			resign()
		}},
		{"commit_vote_alter_consensus_data_hash", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomHash aptos.HashValue
			for i := range randomHash {
				randomHash[i] = byte(rng.Intn(256))
			}
			msg.LedgerInfo.ConsensusDataHash = randomHash
			resign()
		}},
	}, seed)
}

// RoundTimeoutMsg need resigning with the timout sender's SK
// if we mutate any of the following subset of fields (which are the signed payload) in the inner TwoChainTimeout struct:
// - Timeout.Epoch, Timeout.Round, Timeout.QuorumCert.VoteData.Proposed.Round (hqcRound)
//
// There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
// are not immediately discarded by the receiver:
//   - Timeout.Epoch == QC.Proposed.Epoch == receiver_local_epoch == SyncInfo.HQC.Cert.Epoch (all same epoch)
//   - Timeout.Round > QC.certified_block.Round (timing out a round after the QC)
//   - Timeout.Round > SyncInfo.HighestRound (= max(HQC.Cert.Round, highest_timeout_round))
//   - Timeout.Round == receiver_local_round (after sync_up)
//   - Timeout.QuorumCert.Cert.Round (= hqc_round) <= SyncInfo.HighestQuorumCert.Cert.Round
//   - Round++ alone keeps Round > QC.Round; Round-- must shift QC rounds too
//   - Epoch changes must shift Timeout.Epoch and all embedded QC epochs together
func mutateRoundTimeoutMsg(msg *aptos.RoundTimeoutMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) string {
	rng := rand.New(rand.NewSource(uint64(seed)))
	resign := func() {
		sk := keysByAuthor[msg.RoundTimeout.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignRoundTimeout(&msg.RoundTimeout, sk)
	}
	mutations := []mutation{
		// Structure-aware mutations
		{"roundtimeout_change_author", func() {
			current := msg.RoundTimeout.Author
			others := make([]aptos.AccountAddress, 0)
			for k := range keysByAuthor {
				if k != current {
					others = append(others, k)
				}
			}
			if len(others) > 0 {
				msg.RoundTimeout.Author = others[rng.Intn(len(others))]
				resign()
			}
		}},
		{"roundtimeout_change_reason", func() {
			current := msg.RoundTimeout.Reason
			numValidators := len(keysByAuthor)
			numBytes := (numValidators + 7) / 8
			bitmask := make([]byte, numBytes)
			for i := 0; i < numValidators; i++ {
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
		{"roundtimeout_qc_proposed_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ID = randomID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_grandparent_id_swap", func() {
			rng := rand.New(rand.NewSource(uint64(seed)))
			var randomID aptos.HashValue
			for i := range randomID {
				randomID[i] = byte(rng.Intn(256))
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ID = randomID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hcc_executed_state", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_inject_next_epoch_state", func() {
			proposed := &msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed
			proposed.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    proposed.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_timestamp_short_future", func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.TimestampUsecs += 500_000
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_swap_with_syncinfo_hcc", func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			hcc := &msg.SyncInfo.HighestCommitCert
			msg.RoundTimeout.Timeout.QuorumCert = aptos.QuorumCert{
				VoteData:         hcc.VoteData,
				SignedLedgerInfo: hcc.SignedLedgerInfo,
			}
			resign()
		}},
	}

	mutations = append(mutations, syncInfoMutations(&msg.SyncInfo, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

// Helpers
func sameReason(a, b aptos.RoundTimeoutReason) bool {
	return (a.Unknown != nil && b.Unknown != nil) ||
		(a.ProposalNotReceived != nil && b.ProposalNotReceived != nil) ||
		(a.NoQC != nil && b.NoQC != nil) ||
		(a.PayloadUnavailable != nil && b.PayloadUnavailable != nil)
}
