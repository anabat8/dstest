package network

import (
	"fmt"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"golang.org/x/exp/rand"
)

type Mutator interface {
	Mutate(msg aptos.IConsensusMessage, seed int64) (mutation, error)
}

/*
  - keysByAuthor maps validator addresses to their consensus private keys,
    needed for resigning msgs after mutations
  - orderedAddrs are the validator addresses in genesis registration order
    (v0, v1, v2...); used for QC bitmask mutations where we need to know the order of validators
    to produce valid (aggregated) signatures
*/
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

/*
Mutate function applies a random mutation on the given original message based on the seed.
It returns the name of the mutation applied and an error if the consensus msg type is not supported.
The payload is mutated in place.
*/
func (m *AptosMutator) Mutate(cMsg aptos.IConsensusMessage, seed int64) (mutation, error) {
	var mut mutation
	switch v := cMsg.(type) {
	case *aptos.ProposalMsg:
		mut = mutateProposalMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.OptProposalMsg:
		mut = mutateOptProposalMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.VoteMsg:
		mut = mutateVoteMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.CommitMessage:
		mut = mutateCommitMessage(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.CommitVote:
		mut = mutateCommitVote(v, seed, m.keysByAuthor)
	case *aptos.CommitDecision:
		mut = mutateCommitDecision(v, seed, m.keysByAuthor, m.orderedAddrs)
	case *aptos.RoundTimeoutMsg:
		mut = mutateRoundTimeoutMsg(v, seed, m.keysByAuthor, m.orderedAddrs)
	default:
		return mutation{}, fmt.Errorf("unsupported consensus message type: %T", cMsg)
	}

	return mut, nil
}

func (m mutation) ShouldOmitSending() bool {
	return m.Name == OmitMutation.Name && m.Method == OmitMutation.Method
}

type mutation struct {
	Name   string
	Method mutationMethod
	fn     func()
}

var OmitMutation mutation = mutation{Name: "omit_mutation", Method: as, fn: func() {}}

type mutationMethod string

const (
	ss mutationMethod = "small_scope"
	as mutationMethod = "structure_aware"
)

func pickMutation(mutations []mutation, seed int64) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	mutations = append(mutations, OmitMutation)
	chosen := mutations[rng.Intn(len(mutations))]
	chosen.fn()
	return chosen
}

/*
syncInfoMutations returns mutations that operate on a SyncInfo.

Reused by every message type that carries a SyncInfo (ProposalMsg, OptProposalMsg,
VoteMsg, RoundTimeoutMsg);
Mutations that change a QC/WrappedLedgerInfo VoteData also re-sign
the QC/WrappedLedgerInfo aggregate signature (via ResignAggregate / ResignQC / ResignWrappedLedgerInfo).

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - Per QC (in every VoteData): Proposed.Round > Parent.Round, Proposed.Epoch == Parent.Epoch,
    Parent.Version <= Proposed.Version, Parent.Timestamp <= Proposed.Timestamp
  - HQC.round >= HOC.round >= HCC.round
  - HQC, HOC, HCC must be in the same epoch
*/
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
		// Small-scope mutations
		{"syncinfo_hqc_shift_rounds_up", ss, func() {
			bumpRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
		}},
		{"syncinfo_hqc_shift_rounds_down", ss, func() {
			// HQC.round >= HOC.round >= HCC.round; bail out if
			// dropping HQC by 1 would break that ordering with HOC (or with HCC if HOC is None)
			hqcRound := si.HighestQuorumCert.VoteData.Proposed.Round
			var floor aptos.Round
			if si.HighestOrderedCert.Some != nil {
				floor = si.HighestOrderedCert.Some.VoteData.Proposed.Round
			} else {
				floor = si.HighestCommitCert.VoteData.Proposed.Round
			}
			if hqcRound <= floor {
				return
			}
			dropRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_rounds_up", ss, func() {
			bumpRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				bumpRounds(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			bumpRounds(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_rounds_down", ss, func() {
			dropRounds(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				dropRounds(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			dropRounds(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_epochs_up", ss, func() {
			bumpEpochs(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				bumpEpochs(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			bumpEpochs(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_all_qcs_shift_epochs_down", ss, func() {
			dropEpochs(&si.HighestQuorumCert.VoteData, &si.HighestQuorumCert.SignedLedgerInfo)
			if si.HighestOrderedCert.Some != nil {
				dropEpochs(&si.HighestOrderedCert.Some.VoteData, &si.HighestOrderedCert.Some.SignedLedgerInfo)
			}
			dropEpochs(&si.HighestCommitCert.VoteData, &si.HighestCommitCert.SignedLedgerInfo)
		}},
		{"syncinfo_hqc_timestamp_increase", ss, func() {
			si.HighestQuorumCert.VoteData.Proposed.TimestampUsecs += 5_000_000
			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
		// Structure-aware mutations
		{"syncinfo_all_qcs_downgrade_to_commit_block", as, func() {
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
		{"syncinfo_hqc_align_with_hoc", as, func() {
			if si.HighestOrderedCert.Some == nil {
				return
			}
			// Copy both VoteData and SignedLedgerInfo together; preserves HOC's aggregate
			// sig over HOC's LedgerInfo, which is still valid after the copy; No re-sign needed
			si.HighestQuorumCert.VoteData = si.HighestOrderedCert.Some.VoteData
			si.HighestQuorumCert.SignedLedgerInfo = si.HighestOrderedCert.Some.SignedLedgerInfo
		}},
		{"syncinfo_hoc_align_with_hqc", as, func() {
			if si.HighestOrderedCert.Some == nil {
				si.HighestOrderedCert.Some = &aptos.WrappedLedgerInfo{}
			}
			si.HighestOrderedCert.Some.VoteData = si.HighestQuorumCert.VoteData
			si.HighestOrderedCert.Some.SignedLedgerInfo = si.HighestQuorumCert.SignedLedgerInfo
		}},
		{"syncinfo_hcc_align_with_hoc", as, func() {
			if si.HighestOrderedCert.Some == nil {
				return
			}
			si.HighestCommitCert = *si.HighestOrderedCert.Some
		}},
		{"syncinfo_qc_parent_swap_with_commit_id", as, func() {
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
		{"syncinfo_qc_executed_state_swap", as, func() {
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			si.HighestQuorumCert.VoteData.Proposed.ExecutedStateID =
				si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&si.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
	}
}

/*
Mutations for Highest2ChainTimeoutCert (H2CTC) in SyncInfo.

Inject a fabricated TwoChainTimeoutCertificate into SyncInfo
when one isn't already present or modify the inner timeout fields.
Appended only by message types that allow a TC: ProposalMsg, VoteMsg, RoundTimeoutMsg.

Invariants:
  - TC.timeout.qc.cert.round  <  TC.timeout.round (where cert = votedata.proposed) (1)
  - TC.timeout.round  <=  sync_info.HQC.cert.round (2)
*/
func h2ctcMutations(
	si *aptos.SyncInfo,
	rng *rand.Rand,
	keysByAuthor map[aptos.AccountAddress]*aptos.SK,
	orderedAddrs []aptos.AccountAddress,
) []mutation {
	return []mutation{
		// Small-scope mutations
		{"syncinfo_h2ctc_round_shift_up", ss, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			newRound := existing.Timeout.Round + 1
			hqcRound := si.HighestQuorumCert.VoteData.Proposed.Round
			// check invariant (2); new_round must be <= HQC.round
			if newRound > hqcRound {
				return
			}
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				existing.Timeout.Epoch,
				newRound,
				existing.Timeout.QuorumCert,
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_h2ctc_round_shift_down", ss, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			if existing.Timeout.Round <= 1 {
				return
			}
			newRound := existing.Timeout.Round - 1
			// check invariant (1); inner_qc.round < new_round
			if existing.Timeout.QuorumCert.VoteData.Proposed.Round >= newRound {
				return
			}
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				existing.Timeout.Epoch,
				newRound,
				existing.Timeout.QuorumCert,
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		// Structure-aware mutations
		{"syncinfo_inject_h2ctc_with_hcc_qc", as, func() {
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			hccRound := si.HighestCommitCert.VoteData.Proposed.Round
			hqcRound := si.HighestQuorumCert.VoteData.Proposed.Round
			// check invariant (2)
			if hccRound+1 > hqcRound {
				return
			}
			// inject TC; HCC as inner QC; round = HCC.round + 1
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				si.HighestQuorumCert.VoteData.Proposed.Epoch,
				hccRound+1,
				aptos.QuorumCert{
					VoteData:         si.HighestCommitCert.VoteData,
					SignedLedgerInfo: si.HighestCommitCert.SignedLedgerInfo,
				},
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_inject_h2ctc_with_hoc_qc", as, func() {
			if si.HighestOrderedCert.Some == nil ||
				si.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			hocRound := si.HighestOrderedCert.Some.VoteData.Proposed.Round
			hqcRound := si.HighestQuorumCert.VoteData.Proposed.Round
			// check invariant (2)
			if hocRound+1 > hqcRound {
				return
			}
			// inject TC; HOC as inner QC; round = HOC.round + 1
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				si.HighestQuorumCert.VoteData.Proposed.Epoch,
				hocRound+1,
				aptos.QuorumCert{
					VoteData:         si.HighestOrderedCert.Some.VoteData,
					SignedLedgerInfo: si.HighestOrderedCert.Some.SignedLedgerInfo,
				},
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_h2ctc_drop_to_none", as, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{None: &aptos.BcsUnit{}}
		}},
		{"syncinfo_h2ctc_inner_qc_swap_to_hcc", as, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			if si.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			hccRound := si.HighestCommitCert.VoteData.Proposed.Round
			// check invariant (1)
			if hccRound >= existing.Timeout.Round {
				return
			}
			// preserve current tc.round; replace inner QC with HCC
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				existing.Timeout.Epoch,
				existing.Timeout.Round,
				aptos.QuorumCert{
					VoteData:         si.HighestCommitCert.VoteData,
					SignedLedgerInfo: si.HighestCommitCert.SignedLedgerInfo,
				},
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_h2ctc_inner_qc_swap_to_hoc", as, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			if si.HighestOrderedCert.Some == nil ||
				si.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			hocRound := si.HighestOrderedCert.Some.VoteData.Proposed.Round
			// check invariant (1)
			if hocRound >= existing.Timeout.Round {
				return
			}
			// preserve current tc.round; replace inner QC with HOC
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				existing.Timeout.Epoch,
				existing.Timeout.Round,
				aptos.QuorumCert{
					VoteData:         si.HighestOrderedCert.Some.VoteData,
					SignedLedgerInfo: si.HighestOrderedCert.Some.SignedLedgerInfo,
				},
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_h2ctc_inner_qc_swap_to_hqc", as, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			if si.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			hqcRound := si.HighestQuorumCert.VoteData.Proposed.Round
			// check invariant (1)
			if hqcRound >= existing.Timeout.Round {
				return
			}
			// preserve current tc.round; replace inner QC with HQC
			tc, err := aptos.BuildFullQuorumTwoChainTimeoutCert(
				existing.Timeout.Epoch,
				existing.Timeout.Round,
				si.HighestQuorumCert,
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
		{"syncinfo_h2ctc_drop_bitmask_one_bit", as, func() {
			if si.Highest2ChainTimeoutCert == nil || si.Highest2ChainTimeoutCert.Some == nil {
				return
			}
			existing := si.Highest2ChainTimeoutCert.Some
			if existing.SignaturesWithRounds.Sig.Sig == nil ||
				existing.SignaturesWithRounds.Sig.Sig.Some == nil {
				return
			}
			origMask := existing.SignaturesWithRounds.Sig.ValidatorBitmask.Inner
			setBitIndices := make([]int, 0, len(orderedAddrs))
			for i := 0; i < len(orderedAddrs); i++ {
				if int(origMask[i/8])&(1<<(7-uint(i%8))) != 0 {
					setBitIndices = append(setBitIndices, i)
				}
			}
			n := len(orderedAddrs)
			quorum := 2*(n/3) + 1
			if len(setBitIndices) <= quorum {
				return
			}
			drop := setBitIndices[rng.Intn(len(setBitIndices))]
			newBitmask := make([]byte, len(origMask))
			copy(newBitmask, origMask)
			newBitmask[drop/8] &^= 1 << (7 - uint(drop%8))
			tc, err := aptos.BuildTwoChainTimeoutCertWithBitmask(
				existing.Timeout.Epoch,
				existing.Timeout.Round,
				existing.Timeout.QuorumCert,
				newBitmask,
				keysByAuthor,
				orderedAddrs,
			)
			if err != nil {
				return
			}
			si.Highest2ChainTimeoutCert = &aptos.OptionTwoChainTimeoutCertificate{Some: &tc}
		}},
	}
}

/*
ProposalMsg need resigning with the proposer's SK
if the mutation touches any field that is part of the BlockData struct (which is the signed payload)

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - Parent.Round < BlockProposed.Round, where parent of this proposed block is QC.VoteData.Proposed
  - Parent.Epoch == BlockProposed.Epoch == SyncInfo.HQC.Proposed.Epoch
  - Parent.ID == SyncInfo.HQC.Proposed.ID
  - BlockProposed.Round - 1 == max(Parent.Round, SyncInfo.H2CTC.Timeout.Round (if it exists))
  - BlockProposed.Timestamp_usecs > Parent.Timestamp_usecs; for nil/reconfig blocks they should be equal
  - BlockProposed.Timestamp_usecs <= now + 5min (for non-nil, non-reconfig blocks)
  - BlockProposed.Author == sender
  - BlockProposed.Epoch == receiver_local_epoch
*/
func mutateProposalMsg(msg *aptos.ProposalMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
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
		// Small-scope mutations
		{"proposal_large_timestamp_future", ss, func() {
			msg.Proposal.BlockData.TimestampUsecs += 5_000_000
			resign()
		}},
		{"proposal_short_timestamp_future", ss, func() {
			msg.Proposal.BlockData.TimestampUsecs += 500_000
			resign()
		}},
		{"proposal_timestamps_shift_past", ss, func() {
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
		{"proposal_qc_votedata_swap_with_syncinfo_highest_qc_votedata", as, func() {
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
		{"proposal_to_optimistic_proposal", as, func() {
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
		{"proposal_parent_swap_executed_state_with_hcc", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_swap_executed_state_with_hoc", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_swap_executed_state_with_hqc", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_executed_state_random_value", as, func() {
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ExecutedStateID = randomHash(rng)
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_grandparent_swap_executed_state_with_hcc", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_grandparent_swap_executed_state_with_hoc", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_grandparent_swap_executed_state_with_hqc", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_grandparent_executed_state_random_value", as, func() {
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ExecutedStateID = randomHash(rng)
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_inject_next_epoch_state", as, func() {
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
		{"proposal_grandparent_id_swap", as, func() {
			msg.Proposal.BlockData.QuorumCert.VoteData.Parent.ID = randomHash(rng)
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_parent_id_swap", as, func() {
			id := randomHash(rng)
			msg.Proposal.BlockData.QuorumCert.VoteData.Proposed.ID = id
			msg.SyncInfo.HighestQuorumCert.VoteData.Proposed.ID = id
			aptos.ResignQC(&msg.Proposal.BlockData.QuorumCert, keysByAuthor, orderedAddrs)
			aptos.ResignQC(&msg.SyncInfo.HighestQuorumCert, keysByAuthor, orderedAddrs)
			resign()
		}},
		{"proposal_payload_empty", as, func() {
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
	mutations = append(mutations, h2ctcMutations(&msg.SyncInfo, rng, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

/*
OptProposalMsg carries no proposer signature; the message is authenticated through
the grandparent QC's aggregate signature. Mutations that touch the grandparent QC's
(OptBlockData.BlockBody.V0.GrandparentQC) VoteData require ResignQC;
Mutations on plain fields from OptBlockData (like Parent or Timestamp) require no resigning.

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - BlockProposed.Epoch == receiver_local_epoch
  - BlockProposed.Epoch == Parent.Epoch == GrandparentQC.VoteData.Proposed.Epoch == SyncInfo.HQC.VoteData.Proposed.Epoch
  - BlockProposed.Round > 1
  - Strict +1 chain: BlockProposed.Round == Parent.Round + 1 == GrandparentQC.VoteData.Proposed.Round + 2
  - GrandparentQC.VoteData.Proposed.ID == SyncInfo.HQC.VoteData.Proposed.ID
  - !GrandparentQC.VoteData.Proposed.has_reconfiguration() (opt proposals are disallowed after a reconfig event
    which is the event in which the epoch changes)
  - SyncInfo.H2CTC == None (opt proposals can't carry a timeout cert)
  - BlockProposed.TimestampUsecs > Parent.TimestampUsecs > GrandparentQC.VoteData.Proposed.TimestampUsecs
  - BlockProposed.TimestampUsecs <= now + 5min
  - local_hqc.Round + 1 == BlockData.Round
    and local_hqc.ID == BlockData.Parent.ID (receiver's local stored hqc)
*/
func mutateOptProposalMsg(msg *aptos.OptProposalMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	mutations := []mutation{
		// Small-scope mutations
		{"optproposal_large_timestamp_future", ss, func() {
			msg.BlockData.TimestampUsecs += 5_000_000
		}},
		{"optproposal_short_timestamp_future", ss, func() {
			msg.BlockData.TimestampUsecs += 500_000
		}},
		{"optproposal_timestamps_shift_past", ss, func() {
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
		{"optproposal_parent_version_shift_up", ss, func() {
			msg.BlockData.Parent.Version++
		}},
		// Structure-aware mutations
		{"optproposal_parent_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.BlockData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
		}},
		{"optproposal_parent_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.BlockData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
		}},
		{"optproposal_parent_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.BlockData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
		}},
		{"optproposal_parent_executed_state_random_value", as, func() {
			msg.BlockData.Parent.ExecutedStateID = randomHash(rng)
		}},
		{"optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
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
		{"optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_grandparent_qc_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_grandparent_qc_executed_state_random_value", as, func() {
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ExecutedStateID = randomHash(rng)
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_greatgrandparent_id_swap", as, func() {
			// Mutate GrandParentQC.VoteData.Parent.ID (the great-grandparent of the opt block)
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Parent.ID = randomHash(rng)
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_grandparent_qc_proposed_id_swap", as, func() {
			if msg.BlockData.BlockBody == nil || msg.BlockData.BlockBody.V0 == nil {
				return
			}
			id := randomHash(rng)
			msg.BlockData.BlockBody.V0.GrandparentQC.VoteData.Proposed.ID = id
			msg.SyncInfo.HighestQuorumCert.VoteData.Proposed.ID = id
			_ = aptos.ResignQC(&msg.BlockData.BlockBody.V0.GrandparentQC, keysByAuthor, orderedAddrs)
			_ = aptos.ResignQC(&msg.SyncInfo.HighestQuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"optproposal_payload_empty", as, func() {
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

/*
VoteMsg need resigning with the voter's SK
if the mutation touches any field that is part of the VoteData or LedgerInfo structs.

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - VoteData.Proposed.Epoch == receiver_local_epoch
  - VoteData.Proposed.Epoch == SyncInfo.HQC.Cert.Epoch (where .Cert == VoteData.Proposed)
  - VoteData.Proposed.Round > SyncInfo.HighestRound (= max(HQC.Cert.Round, highest_timeout_round))
  - Vote.Author == network sender
  - In VoteData: Parent.Epoch == Proposed.Epoch; Parent.Round < Proposed.Round; Parent.Ts <= Proposed.Ts;
    Proposed.Version == 0 or Parent.Version <= Proposed.Version
  - LedgerInfo.ConsensusDataHash == VoteData.hash()
  - If TwoChainTimeout.Some, Timeout.Qc.HQC.Cert.Round <= SyncInfo.HQC.Cert.Round
  - VoteData.Proposed.Round == receiver_local_round (after sync_up)
  - Receiver must be the leader for VoteData.Proposed.Round + 1 to process the incoming vote
*/
func mutateVoteMsg(msg *aptos.VoteMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	resign := func() {
		sk := keysByAuthor[msg.Vote.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignVote(&msg.Vote, sk)
	}
	mutations := []mutation{
		// Small-scope mutations
		{"vote_parent_version_shift_up", ss, func() {
			msg.Vote.VoteData.Parent.Version++
			resign()
		}},
		{"vote_proposed_version_shift_up", ss, func() {
			msg.Vote.VoteData.Proposed.Version++
			resign()
		}},
		{"vote_proposed_timestamp_large_future", ss, func() {
			msg.Vote.VoteData.Proposed.TimestampUsecs += 5_000_000
			resign()
		}},
		{"vote_proposed_timestamp_short_future", ss, func() {
			msg.Vote.VoteData.Proposed.TimestampUsecs += 500_000
			resign()
		}},
		{"vote_timestamps_shift_past", ss, func() {
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
		{"vote_parent_round_shift_down", ss, func() {
			if msg.Vote.VoteData.Parent.Round == 0 {
				return
			}
			msg.Vote.VoteData.Parent.Round--
			resign()
		}},
		// Structure-aware mutations
		{"vote_proposed_id_swap", as, func() {
			msg.Vote.VoteData.Proposed.ID = randomHash(rng)
			resign()
		}},
		{"vote_parent_id_swap", as, func() {
			msg.Vote.VoteData.Parent.ID = randomHash(rng)
			resign()
		}},
		{"vote_proposed_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_proposed_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_proposed_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_proposed_executed_state_random_value", as, func() {
			msg.Vote.VoteData.Proposed.ExecutedStateID = randomHash(rng)
			resign()
		}},
		{"vote_parent_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_parent_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_parent_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			resign()
		}},
		{"vote_parent_executed_state_random_value", as, func() {
			msg.Vote.VoteData.Parent.ExecutedStateID = randomHash(rng)
			resign()
		}},
		{"vote_proposed_inject_next_epoch_state", as, func() {
			proposed := &msg.Vote.VoteData.Proposed
			proposed.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    proposed.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"vote_parent_inject_next_epoch_state", as, func() {
			parent := &msg.Vote.VoteData.Parent
			parent.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    parent.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"vote_ledger_info_commit_info_swap_with_syncinfo_hcc", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.Vote.LedgerInfo.CommitInfo = msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo
			resign()
		}},
		{"attach_timeout_to_vote", as, func() {
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
		{"attach_timeout_to_vote_with_hcc_qc", as, func() {
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
		{"attach_timeout_to_vote_with_hoc_qc", as, func() {
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
	mutations = append(mutations, h2ctcMutations(&msg.SyncInfo, rng, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

/*
CommitMessage is an enum with 4 variants:
  - Vote(CommitVote): voter signs LedgerInfo; mutations on the LedgerInfo require ResignCommitVote
  - Decision(CommitDecision): aggregate sig over LedgerInfo; mutations on the LedgerInfo
    require ResignCommitDecision (re-aggregates with the same bitmask)
  - Ack(()) / Nack: response-direction unit messages; no signatures

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - For incoming Vote/Decision: CommitMessage.Epoch == receiver_local_epoch
  - For Vote: Vote.Author == network sender
*/
func mutateCommitMessage(msg *aptos.CommitMessage, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	if msg.Decision != nil {
		return mutateCommitDecision(msg.Decision, seed, keysByAuthor, orderedAddrs)
	} else if msg.Ack != nil {
		return pickMutation([]mutation{
			{"commit_swap_ack_with_nack", as, func() {
				msg.Ack = nil
				msg.Nack = &aptos.BcsUnit{}
			}},
		}, seed)
	} else if msg.Nack != nil {
		return pickMutation([]mutation{
			{"commit_swap_nack_with_ack", as, func() {
				msg.Nack = nil
				msg.Ack = &aptos.BcsUnit{}
			}},
		}, seed)
	} else if msg.Vote != nil {
		rng := rand.New(rand.NewSource(uint64(seed)))
		mutations := []mutation{
			{"commit_vote_to_decision_with_full_quorum", as, func() {
				lis, err := aptos.BuildFullQuorumLedgerInfoWithSignatures(
					msg.Vote.LedgerInfo, keysByAuthor, orderedAddrs)
				if err != nil {
					return
				}
				msg.Decision = &aptos.CommitDecision{LedgerInfo: lis}
				msg.Vote = nil
			}},
		}
		mutations = append(mutations, commitVoteMutations(msg.Vote, rng, keysByAuthor)...)
		return pickMutation(mutations, seed)
	}
	return mutation{}
}

func mutateCommitVote(msg *aptos.CommitVote, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	return pickMutation(commitVoteMutations(msg, rng, keysByAuthor), seed)
}

/*
Returns a list of mutations for CommitVote.
Used by plain CommitVote messages and also CommitMessage.
Mutations that touch any field in the inner struct LedgerInfo
require resigning the commit vote with the voter's SK.
*/
func commitVoteMutations(msg *aptos.CommitVote, rng *rand.Rand, keysByAuthor map[aptos.AccountAddress]*aptos.SK) []mutation {
	resign := func() {
		sk := keysByAuthor[msg.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignCommitVote(msg, sk)
	}
	return []mutation{
		// Small-scope mutations
		{"commit_vote_alter_commit_info_version_increment", ss, func() {
			msg.LedgerInfo.CommitInfo.Version++
			resign()
		}},
		{"commit_vote_alter_commit_info_version_decrement", ss, func() {
			if msg.LedgerInfo.CommitInfo.Version == 0 {
				return
			}
			msg.LedgerInfo.CommitInfo.Version--
			resign()
		}},
		// Structure-aware mutations
		{"commit_vote_alter_commit_info_id", as, func() {
			msg.LedgerInfo.CommitInfo.ID = randomHash(rng)
			resign()
		}},
		{"commit_vote_alter_consensus_data_hash", as, func() {
			msg.LedgerInfo.ConsensusDataHash = randomHash(rng)
			resign()
		}},
		{"commit_vote_alter_commit_info_executed_state_id", as, func() {
			msg.LedgerInfo.CommitInfo.ExecutedStateID = randomHash(rng)
			resign()
		}},
		{"commit_vote_alter_commit_info_inject_next_epoch_state", as, func() {
			ci := &msg.LedgerInfo.CommitInfo
			ci.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    ci.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"commit_vote_alter_commit_info_clear_next_epoch_state", as, func() {
			ci := &msg.LedgerInfo.CommitInfo
			if ci.NextEpochState == nil || ci.NextEpochState.Some == nil {
				return
			}
			ci.NextEpochState = &aptos.OptionEpochState{None: &aptos.BcsUnit{}}
			resign()
		}},
	}
}

func mutateCommitDecision(msg *aptos.CommitDecision, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	return pickMutation(commitDecisionMutations(msg, rng, keysByAuthor, orderedAddrs), seed)
}

/*
Returns a list of mutations for CommitDecision.
Used by plain CommitDecision messages and also CommitMessage.
Mutations that touch any field in the inner struct LedgerInfo require
resigning the commit decision with aggregate signatures.
*/
func commitDecisionMutations(msg *aptos.CommitDecision, rng *rand.Rand, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) []mutation {
	resign := func() {
		_ = aptos.ResignCommitDecision(msg, keysByAuthor, orderedAddrs)
	}
	return []mutation{
		// Small-scope mutations
		{"commit_decision_alter_commit_info_version_increment", ss, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			msg.LedgerInfo.V0.LedgerInfo.CommitInfo.Version++
			resign()
		}},
		{"commit_decision_alter_commit_info_version_decrement", ss, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			if msg.LedgerInfo.V0.LedgerInfo.CommitInfo.Version == 0 {
				return
			}
			msg.LedgerInfo.V0.LedgerInfo.CommitInfo.Version--
			resign()
		}},
		// Structure-aware mutations
		{"commit_decision_alter_consensus_data_hash", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			msg.LedgerInfo.V0.LedgerInfo.ConsensusDataHash = randomHash(rng)
			resign()
		}},
		{"commit_decision_alter_commit_info_id", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			msg.LedgerInfo.V0.LedgerInfo.CommitInfo.ID = randomHash(rng)
			resign()
		}},
		{"commit_decision_alter_commit_info_executed_state_id", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			msg.LedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID = randomHash(rng)
			resign()
		}},
		{"commit_decision_alter_commit_info_inject_next_epoch_state", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			ci := &msg.LedgerInfo.V0.LedgerInfo.CommitInfo
			ci.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    ci.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			resign()
		}},
		{"commit_decision_alter_commit_info_clear_next_epoch_state", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			ci := &msg.LedgerInfo.V0.LedgerInfo.CommitInfo
			if ci.NextEpochState == nil || ci.NextEpochState.Some == nil {
				return
			}
			ci.NextEpochState = &aptos.OptionEpochState{None: &aptos.BcsUnit{}}
			resign()
		}},
		{"commit_decision_drop_bitmask_one_bit", as, func() {
			if msg.LedgerInfo.V0 == nil {
				return
			}
			bm := msg.LedgerInfo.V0.Signatures.ValidatorBitmask.Inner
			setBitIndices := make([]int, 0, len(orderedAddrs))
			for i := 0; i < len(orderedAddrs); i++ {
				if int(bm[i/8])&(1<<(7-uint(i%8))) != 0 {
					setBitIndices = append(setBitIndices, i)
				}
			}
			n := len(orderedAddrs)
			quorum := 2*(n/3) + 1
			if len(setBitIndices) <= quorum {
				return
			}
			drop := setBitIndices[rng.Intn(len(setBitIndices))]
			bm[drop/8] &^= 1 << (7 - uint(drop%8))
			resign()
		}},
	}
}

/*
RoundTimeoutMsg need resigning with the timeout sender's SK
if we mutate any of the following subset of fields (which are the signed payload)
in the inner TwoChainTimeout struct:
- Timeout.Epoch, Timeout.Round, Timeout.QuorumCert.VoteData.Proposed.Round (hqcRound)

There are a few invariants we need to consider when applying our mutations, so that our mutated msgs
are not immediately discarded by the receiver:
  - Timeout.Epoch == QC.Proposed.Epoch == receiver_local_epoch == SyncInfo.HQC.VoteData.Proposed.Epoch
  - Timeout.Round > QC.VoteData.Proposed.Round (timing out a round after the QC)
  - Timeout.Round > SyncInfo.HighestRound (= max(HQC.VoteData.Proposed.Round, highest_timeout_round = Highest2ChainTimeoutCert.Some.Timeout.Round_if_present))
  - Timeout.Round == receiver_local_round (after sync_up)
  - Timeout.QC.VoteData.Proposed.Round (= hqc_round) <= SyncInfo.HQC.VoteData.Proposed.Round
  - Same QC.VoteData invariants apply: Parent.Epoch == Proposed.Epoch; Parent.Round < Proposed.Round; Parent.Ts <= Proposed.Ts;
*/
func mutateRoundTimeoutMsg(msg *aptos.RoundTimeoutMsg, seed int64, keysByAuthor map[aptos.AccountAddress]*aptos.SK, orderedAddrs []aptos.AccountAddress) mutation {
	rng := rand.New(rand.NewSource(uint64(seed)))
	resign := func() {
		sk := keysByAuthor[msg.RoundTimeout.Author]
		if sk == nil {
			return
		}
		_ = aptos.ResignRoundTimeout(&msg.RoundTimeout, sk)
	}

	mutations := []mutation{
		// Small-scope mutations
		{"roundtimeout_qc_proposed_timestamp_short_future", ss, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.TimestampUsecs += 500_000
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_timestamp_large_future", ss, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.TimestampUsecs += 5_000_000
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_timestamp_past", ss, func() {
			const delta = uint64(1_000_000)
			vd := &msg.RoundTimeout.Timeout.QuorumCert.VoteData
			if vd.Proposed.TimestampUsecs <= delta || vd.Parent.TimestampUsecs <= delta {
				return
			}
			vd.Proposed.TimestampUsecs -= delta
			vd.Parent.TimestampUsecs -= delta
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		// Structure-aware mutations
		{"roundtimeout_change_author", as, func() {
			current := msg.RoundTimeout.Author
			others := make([]aptos.AccountAddress, 0)
			for k := range keysByAuthor {
				if k != current {
					others = append(others, k)
				}
			}
			if len(others) > 0 {
				msg.RoundTimeout.Author = others[rng.Intn(len(others))]
				// resign since we have a new author
				resign()
			}
		}},
		{"roundtimeout_change_reason", as, func() {
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
			// don't require resigning since reason is not part of the signed timeout payload
			// and author stays the same
		}},
		{"roundtimeout_qc_proposed_id_swap", as, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ID = randomHash(rng)
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_grandparent_id_swap", as, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ID = randomHash(rng)
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_executed_state_random_value", as, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed.ExecutedStateID = randomHash(rng)
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_inject_next_epoch_state", as, func() {
			proposed := &msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed
			proposed.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    proposed.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_proposed_clear_next_epoch_state", as, func() {
			proposed := &msg.RoundTimeout.Timeout.QuorumCert.VoteData.Proposed
			if proposed.NextEpochState == nil || proposed.NextEpochState.Some == nil {
				return
			}
			proposed.NextEpochState = &aptos.OptionEpochState{None: &aptos.BcsUnit{}}
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hcc_executed_state", as, func() {
			if msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hoc_executed_state", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil ||
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_executed_state_swap_with_syncinfo_hqc_executed_state", as, func() {
			if msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0 == nil {
				return
			}
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ExecutedStateID =
				msg.SyncInfo.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ExecutedStateID
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_executed_state_random_value", as, func() {
			msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent.ExecutedStateID = randomHash(rng)
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_inject_next_epoch_state", as, func() {
			parent := &msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent
			parent.NextEpochState = &aptos.OptionEpochState{
				Some: &aptos.EpochState{
					Epoch:    parent.Epoch + 1,
					Verifier: aptos.ValidatorVerifier{ValidatorInfos: nil},
				},
			}
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_parent_clear_next_epoch_state", as, func() {
			parent := &msg.RoundTimeout.Timeout.QuorumCert.VoteData.Parent
			if parent.NextEpochState == nil || parent.NextEpochState.Some == nil {
				return
			}
			parent.NextEpochState = &aptos.OptionEpochState{None: &aptos.BcsUnit{}}
			_ = aptos.ResignQC(&msg.RoundTimeout.Timeout.QuorumCert, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_drop_bitmask_one_bit", as, func() {
			qc := &msg.RoundTimeout.Timeout.QuorumCert
			if qc.SignedLedgerInfo.V0 == nil {
				return
			}
			bm := qc.SignedLedgerInfo.V0.Signatures.ValidatorBitmask.Inner
			setBitIndices := make([]int, 0, len(orderedAddrs))
			for i := 0; i < len(orderedAddrs); i++ {
				if int(bm[i/8])&(1<<(7-uint(i%8))) != 0 {
					setBitIndices = append(setBitIndices, i)
				}
			}
			n := len(orderedAddrs)
			quorum := 2*(n/3) + 1
			if len(setBitIndices) <= quorum {
				return
			}
			drop := setBitIndices[rng.Intn(len(setBitIndices))]
			bm[drop/8] &^= 1 << (7 - uint(drop%8))
			_ = aptos.ResignQC(qc, keysByAuthor, orderedAddrs)
		}},
		{"roundtimeout_qc_swap_with_syncinfo_hcc", as, func() {
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
		{"roundtimeout_qc_swap_with_syncinfo_hoc", as, func() {
			if msg.SyncInfo.HighestOrderedCert.Some == nil {
				return
			}
			hoc := msg.SyncInfo.HighestOrderedCert.Some
			msg.RoundTimeout.Timeout.QuorumCert = aptos.QuorumCert{
				VoteData:         hoc.VoteData,
				SignedLedgerInfo: hoc.SignedLedgerInfo,
			}
			resign()
		}},
		{"roundtimeout_qc_swap_with_syncinfo_hqc", as, func() {
			msg.RoundTimeout.Timeout.QuorumCert = msg.SyncInfo.HighestQuorumCert
			resign()
		}},
	}

	mutations = append(mutations, syncInfoMutations(&msg.SyncInfo, keysByAuthor, orderedAddrs)...)
	mutations = append(mutations, h2ctcMutations(&msg.SyncInfo, rng, keysByAuthor, orderedAddrs)...)
	return pickMutation(mutations, seed)
}

// Helpers
func sameReason(a, b aptos.RoundTimeoutReason) bool {
	return (a.Unknown != nil && b.Unknown != nil) ||
		(a.ProposalNotReceived != nil && b.ProposalNotReceived != nil) ||
		(a.NoQC != nil && b.NoQC != nil) ||
		(a.PayloadUnavailable != nil && b.PayloadUnavailable != nil)
}

func randomHash(rng *rand.Rand) aptos.HashValue {
	var h aptos.HashValue
	for i := range h {
		h[i] = byte(rng.Intn(256))
	}
	return h
}
