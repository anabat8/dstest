package aptos

import (
	"fmt"

	"github.com/fardream/go-bcs/bcs"
)

// ----------
// Base aliases
// ----------

type Round = uint64
type Version = uint64
type PayloadExecutionLimit = uint64

type AccountAddress [32]byte
type Author = AccountAddress
type HashValue [32]byte
type PublicKey [48]byte
type BLSSignature []byte

// ----
// Debug
// -----
type ProposalMsgPrefix1 struct {
	Proposal BlockPrefix1
}

type ProposalMsgPrefix2 struct {
	Proposal Block
	SyncInfo SyncInfo
}

type BlockPrefix1 struct {
	BlockData BlockDataPrefix1
}

type BlockPrefix2 struct {
	BlockData BlockData
	Signature OptionBLSSignature
}

type BlockDataPrefix1 struct {
	Epoch uint64
}

type BlockDataPrefix2 struct {
	Epoch uint64
	Round uint64
}

type BlockDataPrefix3 struct {
	Epoch          uint64
	Round          uint64
	TimestampUsecs uint64
}

type BlockDataPrefix4 struct {
	Epoch          uint64
	Round          uint64
	TimestampUsecs uint64
	QuorumCert     QuorumCert
}

type BlockDataPrefix5 struct {
	Epoch          uint64
	Round          uint64
	TimestampUsecs uint64
	QuorumCert     QuorumCert
	BlockType      BlockType
}

type QuorumCertPrefix1 struct {
	VoteData VoteData
}

type QuorumCertPrefix2 struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

type LedgerInfoWithV0Prefix1 struct {
	LedgerInfo LedgerInfo
}

type LedgerInfoWithV0Prefix2 struct {
	LedgerInfo LedgerInfo
	Signatures AggregateSignature
}

type LedgerInfoPrefix1 struct {
	CommitInfo BlockInfo
}

type LedgerInfoPrefix2 struct {
	CommitInfo        BlockInfo
	ConsensusDataHash HashValue
}

type BlockInfoPrefix1 struct {
	Epoch uint64
}

type BlockInfoPrefix2 struct {
	Epoch uint64
	Round uint64
}

type BlockInfoPrefix3 struct {
	Epoch uint64
	Round uint64
	ID    HashValue
}

type BlockInfoPrefix4 struct {
	Epoch           uint64
	Round           uint64
	ID              HashValue
	ExecutedStateID HashValue
}

type BlockInfoPrefix5 struct {
	Epoch           uint64
	Round           uint64
	ID              HashValue
	ExecutedStateID HashValue
	Version         uint64
}

type BlockInfoPrefix6 struct {
	Epoch           uint64
	Round           uint64
	ID              HashValue
	ExecutedStateID HashValue
	Version         uint64
	TimestampUsecs  uint64
}

type BlockInfoPrefix7 struct {
	Epoch           uint64
	Round           uint64
	ID              HashValue
	ExecutedStateID HashValue
	Version         uint64
	TimestampUsecs  uint64
	NextEpochState  *EpochState
}

type EpochStatePrefix1 struct {
	Epoch uint64
}

type EpochStatePrefix2 struct {
	Epoch    uint64
	Verifier ValidatorVerifier
}

type ValidatorVerifierPrefix1 struct {
	ValidatorInfos []ValidatorConsensusInfo
}

type ValidatorConsensusInfoPrefix1 struct {
	Address AccountAddress
}

type ValidatorConsensusInfoPrefix2 struct {
	Address   AccountAddress
	PublicKey PublicKey
}

type ValidatorConsensusInfoPrefix3 struct {
	Address     AccountAddress
	PublicKey   PublicKey
	VotingPower uint64
}

type BlockInfoNoNext struct {
	Epoch           uint64
	Round           uint64
	ID              HashValue
	ExecutedStateID HashValue
	Version         uint64
	TimestampUsecs  uint64
}

type LedgerInfoNoNext struct {
	CommitInfo        BlockInfoNoNext
	ConsensusDataHash HashValue
}

func DebugUnmarshal(logf func(string, ...any), name string, data []byte, v any) {
	n, err := bcs.Unmarshal(data, v)
	logf("%s: consumed=%d total=%d remaining=%d err=%v", name, n, len(data), len(data)-n, err)
}

// ------------
// ProposalMsg
// ProposalMsg contains the required information for the proposer election protocol to make
// its choice (typically depends on round and proposer info).
// ------------
type ProposalMsg struct {
	Proposal Block
	SyncInfo SyncInfo
}

// ----------
// OptProposalMsg
// OptProposalMsg contains the optimistic proposal and sync info.
// ----------
type OptProposalMsg struct {
	BlockData OptBlockData
	SyncInfo  SyncInfo
}

// ---------
// VoteMsg
// VoteMsg is the struct that is ultimately sent by the voter in response for receiving a
// proposal.
// ---------
type VoteMsg struct {
	// The container for the vote (VoteData, LedgerInfo, Signature)
	Vote Vote
	// Sync info carries information about highest QC, TC and LedgerInfo
	SyncInfo SyncInfo
}

// ---------
// CommitVoteMsg
// CommitVoteMsg is the struct that is sent by the validator after execution to propose
// on the committed state hash root.
// ---------
type CommitVote struct {
	Author     Author
	LedgerInfo LedgerInfo
	Signature  SignatureWithStatus
}

// ---------
// CommitMessage
// ---------
type CommitMessage struct {
	Vote     *CommitVote
	Decision *CommitDecision
	Ack      *BcsUnit
	Nack     *BcsUnit
}

func (CommitMessage) IsBcsEnum() {}

// ---------
// RoundTimeoutMsg
// RoundTimeoutMsg is broadcasted by a validator once it decides to timeout the current round.
// ---------
type RoundTimeoutMsg struct {
	// The container for the vote (VoteData, LedgerInfo, Signature)
	RoundTimeout RoundTimeout
	// Sync info carries information about highest QC, TC and LedgerInfo
	SyncInfo SyncInfo
}

// ---------
// CommitDecision
// ---------
type CommitDecision struct {
	LedgerInfo LedgerInfoWithSignatures
}

// ---------
// Round timeouts
// ---------
type RoundTimeout struct {
	Timeout   TwoChainTimeout
	Author    Author
	Reason    *RoundTimeoutReason
	Signature BLSSignature
}

type RoundTimeoutReason struct {
	Unknown             *BcsUnit
	ProposalNotReceived *BcsUnit
	PayloadUnavailable  *RoundTimeoutReasonPayloadUnavailable
	NoQC                *BcsUnit
}

func (RoundTimeoutReason) IsBcsEnum() {}

type RoundTimeoutReasonPayloadUnavailable struct {
	MissingAuthors BitVec
}

// ----------
// Block
// ----------

type Block struct {
	BlockData BlockData
	Signature *OptionBLSSignature
}

type BlockData struct {
	Epoch          uint64
	Round          Round
	TimestampUsecs uint64
	QuorumCert     QuorumCert
	BlockType      BlockType
}

type OptionBLSSignature struct {
	None *BcsUnit
	Some *BLSSignature
}

func (OptionBLSSignature) IsBcsEnum() {}

// Same as BlockData, without QC and with parent id
type OptBlockData struct {
	Epoch          uint64
	Round          Round
	TimestampUsecs uint64
	Parent         BlockInfo
	BlockBody      *OptBlockBody
}

// ----------
// SyncInfo
// ----------

type SyncInfo struct {
	HighestQuorumCert        QuorumCert
	HighestOrderedCert       *OptionWrappedLedgerInfo
	HighestCommitCert        WrappedLedgerInfo
	Highest2ChainTimeoutCert *OptionTwoChainTimeoutCertificate
}

// ---------
// Vote
// ---------

type Vote struct {
	VoteData        VoteData
	Author          Author
	LedgerInfo      LedgerInfo
	Signature       SignatureWithStatus
	TwoChainTimeout *OptionTwoChainTimeoutWithSig
}

// --------
// Signatures
// --------
type SignatureWithStatus struct {
	Signature BLSSignature
}

// ----------
// QC / vote data
// ----------

type QuorumCert struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

type VoteData struct {
	Proposed BlockInfo
	Parent   BlockInfo
}

type BlockInfo struct {
	Epoch           uint64
	Round           Round
	ID              HashValue
	ExecutedStateID HashValue
	Version         Version
	TimestampUsecs  uint64
	NextEpochState  *OptionEpochState
}

// ----------
// Epoch state / validator verifier
// ----------

type OptionEpochState struct {
	None *BcsUnit
	Some *EpochState
}

func (OptionEpochState) IsBcsEnum() {}

type EpochState struct {
	Epoch    uint64
	Verifier ValidatorVerifier
}

type ValidatorVerifier struct {
	ValidatorInfos []ValidatorConsensusInfo
}

type ValidatorConsensusInfo struct {
	Address     AccountAddress
	PublicKey   PublicKey
	VotingPower uint64
}

// ----------
// Ledger infos
// ----------

type LedgerInfoWithSignatures struct {
	V0 *LedgerInfoWithV0
}

func (LedgerInfoWithSignatures) IsBcsEnum() {}

type LedgerInfoWithV0 struct {
	LedgerInfo LedgerInfo
	Signatures AggregateSignature
}

type LedgerInfo struct {
	CommitInfo        BlockInfo
	ConsensusDataHash HashValue
}

type OptionWrappedLedgerInfo struct {
	None *BcsUnit
	Some *WrappedLedgerInfo
}

func (OptionWrappedLedgerInfo) IsBcsEnum() {}

type WrappedLedgerInfo struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

// ----------
// Timeout certs
// ----------

type OptionTwoChainTimeoutWithSig struct {
	None *BcsUnit
	Some *TwoChainTimeoutWithSig
}

func (OptionTwoChainTimeoutWithSig) IsBcsEnum() {}

type TwoChainTimeoutWithSig struct {
	Timeout   TwoChainTimeout
	Signature BLSSignature
}

type OptionTwoChainTimeoutCertificate struct {
	None *BcsUnit
	Some *TwoChainTimeoutCertificate
}

func (OptionTwoChainTimeoutCertificate) IsBcsEnum() {}

type TwoChainTimeoutCertificate struct {
	Timeout              TwoChainTimeout
	SignaturesWithRounds AggregateSignatureWithRounds
}

type TwoChainTimeout struct {
	Epoch      uint64
	Round      Round
	QuorumCert QuorumCert
}

type AggregateSignatureWithRounds struct {
	Sig    AggregateSignature
	Rounds []Round
}

type AggregateSignature struct {
	ValidatorBitmask BitVec
	Sig              *OptionBLSSignature
}

type BitVec struct {
	Inner []byte
}

// ----------
// BlockType enum
// ----------

type BlockType struct {
	Proposal           *BlockTypeProposal
	NilBlock           *BlockTypeNilBlock
	Genesis            *BcsUnit
	ProposalExt        *ProposalExt
	OptimisticProposal *OptBlockBody
	// DAGBlock skipped in Rust deserialization, so we omit it here
}

func (BlockType) IsBcsEnum() {}

type BlockTypeProposal struct {
	Payload       Payload
	Author        Author
	FailedAuthors []RoundAuthorPair
}

type BlockTypeNilBlock struct {
	FailedAuthors []RoundAuthorPair
}

type RoundAuthorPair struct {
	Field0 Round
	Field1 Author
}

// ----------
// Payload enum
// ----------

type Payload struct {
	DirectMempool             *[]SignedTransaction
	InQuorumStore             *ProofWithData
	InQuorumStoreWithLimit    *ProofWithDataWithTxnLimit
	QuorumStoreInlineHybrid   *QuorumStoreInlineHybrid
	OptQuorumStore            *OptQuorumStorePayload
	QuorumStoreInlineHybridV2 *QuorumStoreInlineHybridV2
}

func (Payload) IsBcsEnum() {}

type QuorumStoreInlineHybrid struct {
	Field0 []BatchInfoSignedTransactionsPair
	Field1 ProofWithData
	Field2 *OptionUint64
}

type OptionUint64 struct {
	None *BcsUnit
	Some *uint64
}

func (OptionUint64) IsBcsEnum() {}

type QuorumStoreInlineHybridV2 struct {
	Field0 []BatchInfoSignedTransactionsPair
	Field1 ProofWithData
	Field2 PayloadExecutionLimit
}

type BatchInfoSignedTransactionsPair struct {
	Field0 BatchInfo
	Field1 []SignedTransaction
}

// ----------
// ProposalExt enum
// ----------

type ProposalExt struct {
	V0 *ProposalExtV0
}

func (ProposalExt) IsBcsEnum() {}

type ProposalExtV0 struct {
	ValidatorTxns []ValidatorTransaction
	Payload       Payload
	Author        Author
	FailedAuthors []RoundAuthorPair
}

// ----------
// OptBlockBody enum
// ----------

type OptBlockBody struct {
	V0 *OptBlockBodyV0
}

func (OptBlockBody) IsBcsEnum() {}

type OptBlockBodyV0 struct {
	ValidatorTxns []ValidatorTransaction
	Payload       Payload
	Author        Author
	GrandparentQC QuorumCert
}

type SignedTransaction struct{}
type ProofWithData struct{}
type ProofWithDataWithTxnLimit struct{}
type OptQuorumStorePayload struct{}
type BatchInfo struct{}
type ValidatorTransaction struct{}

// -------------
// Pretty prints
// -------------

func PrettyPrintProposalMsg(p *ProposalMsg) {
	if p == nil {
		fmt.Println("ProposalMsg: <nil>")
		return
	}

	fmt.Println("========== ProposalMsg ==========")

	// -------------------
	// BlockData
	// -------------------
	bd := p.Proposal.BlockData

	fmt.Printf("Block:\n")
	fmt.Printf("  Epoch: %d\n", bd.Epoch)
	fmt.Printf("  Round: %d\n", bd.Round)
	fmt.Printf("  TimestampUsecs: %d\n", bd.TimestampUsecs)

	// -------------------
	// QuorumCert
	// -------------------
	qc := bd.QuorumCert

	fmt.Println("  QuorumCert:")

	// VoteData
	vd := qc.VoteData

	fmt.Println("    VoteData:")

	fmt.Printf("      Proposed:\n")
	fmt.Printf("        Epoch: %d\n", vd.Proposed.Epoch)
	fmt.Printf("        Round: %d\n", vd.Proposed.Round)
	fmt.Printf("        Version: %d\n", vd.Proposed.Version)
	fmt.Printf("        Timestamp: %d\n", vd.Proposed.TimestampUsecs)

	fmt.Printf("      Parent:\n")
	fmt.Printf("        Epoch: %d\n", vd.Parent.Epoch)
	fmt.Printf("        Round: %d\n", vd.Parent.Round)
	fmt.Printf("        Version: %d\n", vd.Parent.Version)
	fmt.Printf("        Timestamp: %d\n", vd.Parent.TimestampUsecs)

	// -------------------
	// LedgerInfo
	// -------------------
	li := qc.SignedLedgerInfo.V0

	if li != nil {
		fmt.Println("    LedgerInfo:")

		ci := li.LedgerInfo.CommitInfo

		fmt.Printf("      CommitInfo:\n")
		fmt.Printf("        Epoch: %d\n", ci.Epoch)
		fmt.Printf("        Round: %d\n", ci.Round)
		fmt.Printf("        Version: %d\n", ci.Version)
		fmt.Printf("        Timestamp: %d\n", ci.TimestampUsecs)
	}

	// -------------------
	// SyncInfo
	// -------------------
	si := p.SyncInfo

	fmt.Println("SyncInfo:")

	fmt.Printf("  HighestQC Round: %d\n",
		si.HighestQuorumCert.VoteData.Proposed.Round)

	fmt.Printf("  HighestCommitCert Round: %d\n",
		si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)

	if si.HighestOrderedCert != nil {
		if si.HighestOrderedCert.Some != nil {
			fmt.Printf("  HighestOrderedCert Round: %d\n",
				si.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		} else {
			fmt.Printf("  HighestOrderedCert: None\n")
		}
	} else {
		fmt.Printf("  HighestOrderedCert: nil\n")
	}

	if si.Highest2ChainTimeoutCert != nil {
		if si.Highest2ChainTimeoutCert.Some != nil {
			fmt.Printf("  TimeoutCert Round: %d\n",
				si.Highest2ChainTimeoutCert.Some.Timeout.Round)
		} else {
			fmt.Printf("  TimeoutCert: None\n")
		}
	} else {
		fmt.Printf("  TimeoutCert: nil\n")
	}

	fmt.Println("=================================")
}

func PrettyPrintOptProposalMsg(p *OptProposalMsg) {
	if p == nil {
		fmt.Println("OptProposalMsg: <nil>")
		return
	}

	fmt.Println("======= OptProposalMsg =======")

	// -------------------
	// BlockData
	// -------------------
	bd := p.BlockData

	fmt.Println("BlockData:")
	fmt.Printf("  Epoch: %d\n", bd.Epoch)
	fmt.Printf("  Round: %d\n", bd.Round)
	fmt.Printf("  TimestampUsecs: %d\n", bd.TimestampUsecs)

	fmt.Println("  Parent:")
	fmt.Printf("    Epoch: %d\n", bd.Parent.Epoch)
	fmt.Printf("    Round: %d\n", bd.Parent.Round)
	fmt.Printf("    Version: %d\n", bd.Parent.Version)
	fmt.Printf("    Timestamp: %d\n", bd.Parent.TimestampUsecs)
	fmt.Printf("    ID: %s\n", shortHash(bd.Parent.ID))

	// -------------------
	// BlockBody
	// -------------------
	fmt.Println("  BlockBody:")
	if bd.BlockBody == nil {
		fmt.Println("    <nil>")
	} else if bd.BlockBody.V0 != nil {
		body := bd.BlockBody.V0

		fmt.Println("    V0:")
		fmt.Printf("      ValidatorTxns: %d\n", len(body.ValidatorTxns))
		fmt.Printf("      Author: %s\n", shortHash(body.Author))

		fmt.Println("      GrandparentQC:")
		fmt.Printf("        Proposed Round: %d\n", body.GrandparentQC.VoteData.Proposed.Round)
		fmt.Printf("        Proposed ID: %s\n", shortHash(body.GrandparentQC.VoteData.Proposed.ID))
		fmt.Printf("        Parent Round: %d\n", body.GrandparentQC.VoteData.Parent.Round)
		fmt.Printf("        Parent ID: %s\n", shortHash(body.GrandparentQC.VoteData.Parent.ID))

		if body.GrandparentQC.SignedLedgerInfo.V0 != nil {
			fmt.Printf("        Commit Round: %d\n",
				body.GrandparentQC.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
			fmt.Printf("        Commit ID: %s\n",
				shortHash(body.GrandparentQC.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
		}

		fmt.Println("      Payload:")
		switch {
		case body.Payload.DirectMempool != nil:
			fmt.Printf("        DirectMempool: %d txns\n", len(*body.Payload.DirectMempool))
		case body.Payload.InQuorumStore != nil:
			fmt.Println("        InQuorumStore")
		case body.Payload.InQuorumStoreWithLimit != nil:
			fmt.Println("        InQuorumStoreWithLimit")
		case body.Payload.QuorumStoreInlineHybrid != nil:
			fmt.Printf("        QuorumStoreInlineHybrid: %d batches\n",
				len(body.Payload.QuorumStoreInlineHybrid.Field0))
		case body.Payload.OptQuorumStore != nil:
			fmt.Println("        OptQuorumStore")
		case body.Payload.QuorumStoreInlineHybridV2 != nil:
			fmt.Printf("        QuorumStoreInlineHybridV2: %d batches\n",
				len(body.Payload.QuorumStoreInlineHybridV2.Field0))
		default:
			fmt.Println("        <unknown>")
		}
	} else {
		fmt.Println("    <unknown variant>")
	}

	// -------------------
	// SyncInfo
	// -------------------
	si := p.SyncInfo

	fmt.Println("SyncInfo:")

	fmt.Printf("  HighestQC Proposed Round: %d\n",
		si.HighestQuorumCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestQC Proposed ID: %s\n",
		shortHash(si.HighestQuorumCert.VoteData.Proposed.ID))
	fmt.Printf("  HighestQC Parent Round: %d\n",
		si.HighestQuorumCert.VoteData.Parent.Round)
	fmt.Printf("  HighestQC Parent ID: %s\n",
		shortHash(si.HighestQuorumCert.VoteData.Parent.ID))

	if si.HighestQuorumCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestQC Commit Round: %d\n",
			si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestQC Commit ID: %s\n",
			shortHash(si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	fmt.Printf("  HighestCommitCert Proposed Round: %d\n",
		si.HighestCommitCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestCommitCert Proposed ID: %s\n",
		shortHash(si.HighestCommitCert.VoteData.Proposed.ID))

	if si.HighestCommitCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestCommitCert Commit Round: %d\n",
			si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestCommitCert Commit ID: %s\n",
			shortHash(si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	if si.HighestOrderedCert != nil {
		if si.HighestOrderedCert.Some != nil {
			fmt.Printf("  HighestOrderedCert Proposed Round: %d\n",
				si.HighestOrderedCert.Some.VoteData.Proposed.Round)
			fmt.Printf("  HighestOrderedCert Proposed ID: %s\n",
				shortHash(si.HighestOrderedCert.Some.VoteData.Proposed.ID))

			if si.HighestOrderedCert.Some.SignedLedgerInfo.V0 != nil {
				fmt.Printf("  HighestOrderedCert Commit Round: %d\n",
					si.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
				fmt.Printf("  HighestOrderedCert Commit ID: %s\n",
					shortHash(si.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
			}
		} else {
			fmt.Printf("  HighestOrderedCert: None\n")
		}
	} else {
		fmt.Printf("  HighestOrderedCert: nil\n")
	}

	if si.Highest2ChainTimeoutCert != nil {
		if si.Highest2ChainTimeoutCert.Some != nil {
			fmt.Printf("  TimeoutCert Round: %d\n",
				si.Highest2ChainTimeoutCert.Some.Timeout.Round)

			fmt.Printf("  TimeoutCert QC Proposed Round: %d\n",
				si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.Round)
			fmt.Printf("  TimeoutCert QC Proposed ID: %s\n",
				shortHash(si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.ID))
		} else {
			fmt.Printf("  TimeoutCert: None\n")
		}
	} else {
		fmt.Printf("  TimeoutCert: nil\n")
	}

	fmt.Println("==============================")
}

func PrettyPrintVoteMsg(m *VoteMsg) {
	if m == nil {
		fmt.Println("VoteMsg: <nil>")
		return
	}

	fmt.Println("========= VoteMsg =========")

	// -------------------
	// Vote
	// -------------------
	v := m.Vote

	fmt.Println("Vote:")

	fmt.Println("  VoteData:")
	fmt.Println("    Proposed:")
	fmt.Printf("      Epoch: %d\n", v.VoteData.Proposed.Epoch)
	fmt.Printf("      Round: %d\n", v.VoteData.Proposed.Round)
	fmt.Printf("      Version: %d\n", v.VoteData.Proposed.Version)
	fmt.Printf("      Timestamp: %d\n", v.VoteData.Proposed.TimestampUsecs)
	fmt.Printf("      ID: %s\n", shortHash(v.VoteData.Proposed.ID))

	fmt.Println("    Parent:")
	fmt.Printf("      Epoch: %d\n", v.VoteData.Parent.Epoch)
	fmt.Printf("      Round: %d\n", v.VoteData.Parent.Round)
	fmt.Printf("      Version: %d\n", v.VoteData.Parent.Version)
	fmt.Printf("      Timestamp: %d\n", v.VoteData.Parent.TimestampUsecs)
	fmt.Printf("      ID: %s\n", shortHash(v.VoteData.Parent.ID))

	fmt.Printf("  Author: %s\n", shortHash(v.Author))

	fmt.Println("  LedgerInfo:")
	fmt.Println("    CommitInfo:")
	fmt.Printf("      Epoch: %d\n", v.LedgerInfo.CommitInfo.Epoch)
	fmt.Printf("      Round: %d\n", v.LedgerInfo.CommitInfo.Round)
	fmt.Printf("      Version: %d\n", v.LedgerInfo.CommitInfo.Version)
	fmt.Printf("      Timestamp: %d\n", v.LedgerInfo.CommitInfo.TimestampUsecs)
	fmt.Printf("      ID: %s\n", shortHash(v.LedgerInfo.CommitInfo.ID))

	fmt.Println("  Signature:")
	if len(v.Signature.Signature) == 0 {
		fmt.Println("    <empty>")
	} else {
		fmt.Printf("    Len: %d bytes\n", len(v.Signature.Signature))
	}

	fmt.Println("  TwoChainTimeout:")
	if v.TwoChainTimeout.None != nil {
		fmt.Println("    None")
	} else if v.TwoChainTimeout.Some != nil {
		t := v.TwoChainTimeout.Some

		fmt.Printf("    Timeout Epoch: %d\n", t.Timeout.Epoch)
		fmt.Printf("    Timeout Round: %d\n", t.Timeout.Round)

		fmt.Println("    Timeout QC:")
		fmt.Printf("      Proposed Round: %d\n", t.Timeout.QuorumCert.VoteData.Proposed.Round)
		fmt.Printf("      Proposed ID: %s\n", shortHash(t.Timeout.QuorumCert.VoteData.Proposed.ID))
		fmt.Printf("      Parent Round: %d\n", t.Timeout.QuorumCert.VoteData.Parent.Round)
		fmt.Printf("      Parent ID: %s\n", shortHash(t.Timeout.QuorumCert.VoteData.Parent.ID))

		if t.Timeout.QuorumCert.SignedLedgerInfo.V0 != nil {
			fmt.Printf("      Commit Round: %d\n",
				t.Timeout.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
			fmt.Printf("      Commit ID: %s\n",
				shortHash(t.Timeout.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
		}

		fmt.Printf("    Timeout Signature Len: %d bytes\n", len(t.Signature))
	} else {
		fmt.Println("    <nil>")
	}

	// -------------------
	// SyncInfo
	// -------------------
	si := m.SyncInfo

	fmt.Println("SyncInfo:")

	fmt.Printf("  HighestQC Proposed Round: %d\n",
		si.HighestQuorumCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestQC Proposed ID: %s\n",
		shortHash(si.HighestQuorumCert.VoteData.Proposed.ID))
	fmt.Printf("  HighestQC Parent Round: %d\n",
		si.HighestQuorumCert.VoteData.Parent.Round)
	fmt.Printf("  HighestQC Parent ID: %s\n",
		shortHash(si.HighestQuorumCert.VoteData.Parent.ID))

	if si.HighestQuorumCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestQC Commit Round: %d\n",
			si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestQC Commit ID: %s\n",
			shortHash(si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	fmt.Printf("  HighestCommitCert Proposed Round: %d\n",
		si.HighestCommitCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestCommitCert Proposed ID: %s\n",
		shortHash(si.HighestCommitCert.VoteData.Proposed.ID))

	if si.HighestCommitCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestCommitCert Commit Round: %d\n",
			si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestCommitCert Commit ID: %s\n",
			shortHash(si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	if si.HighestOrderedCert != nil {
		if si.HighestOrderedCert.Some != nil {
			fmt.Printf("  HighestOrderedCert Proposed Round: %d\n",
				si.HighestOrderedCert.Some.VoteData.Proposed.Round)
			fmt.Printf("  HighestOrderedCert Proposed ID: %s\n",
				shortHash(si.HighestOrderedCert.Some.VoteData.Proposed.ID))

			if si.HighestOrderedCert.Some.SignedLedgerInfo.V0 != nil {
				fmt.Printf("  HighestOrderedCert Commit Round: %d\n",
					si.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
				fmt.Printf("  HighestOrderedCert Commit ID: %s\n",
					shortHash(si.HighestOrderedCert.Some.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
			}
		} else {
			fmt.Println("  HighestOrderedCert: None")
		}
	} else {
		fmt.Println("  HighestOrderedCert: nil")
	}

	if si.Highest2ChainTimeoutCert != nil {
		if si.Highest2ChainTimeoutCert.Some != nil {
			fmt.Printf("  TimeoutCert Round: %d\n",
				si.Highest2ChainTimeoutCert.Some.Timeout.Round)
			fmt.Printf("  TimeoutCert QC Proposed Round: %d\n",
				si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.Round)
			fmt.Printf("  TimeoutCert QC Proposed ID: %s\n",
				shortHash(si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.ID))
		} else {
			fmt.Println("  TimeoutCert: None")
		}
	} else {
		fmt.Println("  TimeoutCert: nil")
	}

	fmt.Println("============================")
}

func PrettyPrintCommitVote(v *CommitVote) {
	if v == nil {
		fmt.Println("CommitVote: <nil>")
		return
	}

	fmt.Println("======= CommitVote =======")

	// -------------------
	// Author
	// -------------------
	fmt.Printf("Author: %s\n", shortHash(v.Author))

	// -------------------
	// LedgerInfo
	// -------------------
	fmt.Println("LedgerInfo:")
	fmt.Println("  CommitInfo:")
	fmt.Printf("    Epoch: %d\n", v.LedgerInfo.CommitInfo.Epoch)
	fmt.Printf("    Round: %d\n", v.LedgerInfo.CommitInfo.Round)
	fmt.Printf("    Version: %d\n", v.LedgerInfo.CommitInfo.Version)
	fmt.Printf("    Timestamp: %d\n", v.LedgerInfo.CommitInfo.TimestampUsecs)
	fmt.Printf("    ID: %s\n", shortHash(v.LedgerInfo.CommitInfo.ID))

	// -------------------
	// Signature
	// -------------------
	fmt.Println("Signature:")
	if len(v.Signature.Signature) == 0 {
		fmt.Println("  <empty>")
	} else {
		fmt.Printf("  Len: %d bytes\n", len(v.Signature.Signature))
	}

	fmt.Println("==========================")
}

func PrettyPrintCommitMessage(m *CommitMessage) {
	if m == nil {
		fmt.Println("CommitMessage: <nil>")
		return
	}

	fmt.Println("======= CommitMessage =======")

	switch {
	case m.Vote != nil:
		fmt.Println("Variant: Vote")
		PrettyPrintCommitVote(m.Vote)

	case m.Decision != nil:
		fmt.Println("Variant: Decision")
		if m.Decision.LedgerInfo.V0 != nil {
			ci := m.Decision.LedgerInfo.V0.LedgerInfo.CommitInfo
			fmt.Printf("  Commit Round: %d\n", ci.Round)
			fmt.Printf("  Commit ID: %s\n", shortHash(ci.ID))
		}

	case m.Ack != nil:
		fmt.Println("Variant: Ack")

	case m.Nack != nil:
		fmt.Println("Variant: Nack")

	default:
		fmt.Println("Variant: <unknown / none set>")
	}

	fmt.Println("================================")
}

func PrettyPrintRoundTimeoutMsg(m *RoundTimeoutMsg) {
	if m == nil {
		fmt.Println("RoundTimeoutMsg: <nil>")
		return
	}

	fmt.Println("======= RoundTimeoutMsg =======")

	// -------------------
	// RoundTimeout
	// -------------------
	rt := m.RoundTimeout

	fmt.Println("RoundTimeout:")

	// Timeout core
	fmt.Printf("  Epoch: %d\n", rt.Timeout.Epoch)
	fmt.Printf("  Round: %d\n", rt.Timeout.Round)

	fmt.Println("  Timeout QC:")
	fmt.Printf("    Proposed Round: %d\n", rt.Timeout.QuorumCert.VoteData.Proposed.Round)
	fmt.Printf("    Proposed ID: %s\n", shortHash(rt.Timeout.QuorumCert.VoteData.Proposed.ID))
	fmt.Printf("    Parent Round: %d\n", rt.Timeout.QuorumCert.VoteData.Parent.Round)
	fmt.Printf("    Parent ID: %s\n", shortHash(rt.Timeout.QuorumCert.VoteData.Parent.ID))

	if rt.Timeout.QuorumCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("    Commit Round: %d\n",
			rt.Timeout.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("    Commit ID: %s\n",
			shortHash(rt.Timeout.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	// Author
	fmt.Printf("  Author: %s\n", shortHash(rt.Author))

	// Reason
	fmt.Println("  Reason:")
	if rt.Reason == nil {
		fmt.Println("    <nil>")
	} else {
		switch {
		case rt.Reason.Unknown != nil:
			fmt.Println("    Unknown")

		case rt.Reason.ProposalNotReceived != nil:
			fmt.Println("    ProposalNotReceived")

		case rt.Reason.PayloadUnavailable != nil:
			ma := rt.Reason.PayloadUnavailable.MissingAuthors
			fmt.Printf("    PayloadUnavailable: missing_authors_len=%d\n", len(ma.Inner))
			fmt.Printf("    MissingAuthors(bitvec): %x\n", ma.Inner)

		case rt.Reason.NoQC != nil:
			fmt.Println("    NoQC")

		default:
			fmt.Println("    <unknown variant>")
		}
	}

	// Signature
	fmt.Println("  Signature:")
	if len(rt.Signature) == 0 {
		fmt.Println("    <empty>")
	} else {
		fmt.Printf("    Len: %d bytes\n", len(rt.Signature))
	}

	// -------------------
	// SyncInfo
	// -------------------
	si := m.SyncInfo

	fmt.Println("SyncInfo:")

	fmt.Printf("  HighestQC Proposed Round: %d\n",
		si.HighestQuorumCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestQC Proposed ID: %s\n",
		shortHash(si.HighestQuorumCert.VoteData.Proposed.ID))

	if si.HighestQuorumCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestQC Commit Round: %d\n",
			si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestQC Commit ID: %s\n",
			shortHash(si.HighestQuorumCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	fmt.Printf("  HighestCommitCert Proposed Round: %d\n",
		si.HighestCommitCert.VoteData.Proposed.Round)
	fmt.Printf("  HighestCommitCert Proposed ID: %s\n",
		shortHash(si.HighestCommitCert.VoteData.Proposed.ID))

	if si.HighestCommitCert.SignedLedgerInfo.V0 != nil {
		fmt.Printf("  HighestCommitCert Commit Round: %d\n",
			si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.Round)
		fmt.Printf("  HighestCommitCert Commit ID: %s\n",
			shortHash(si.HighestCommitCert.SignedLedgerInfo.V0.LedgerInfo.CommitInfo.ID))
	}

	if si.Highest2ChainTimeoutCert != nil && si.Highest2ChainTimeoutCert.Some != nil {
		fmt.Printf("  TimeoutCert Round: %d\n",
			si.Highest2ChainTimeoutCert.Some.Timeout.Round)
		fmt.Printf("  TimeoutCert QC Proposed Round: %d\n",
			si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.Round)
		fmt.Printf("  TimeoutCert QC Proposed ID: %s\n",
			shortHash(si.Highest2ChainTimeoutCert.Some.Timeout.QuorumCert.VoteData.Proposed.ID))
	} else {
		fmt.Println("  TimeoutCert: None/nil")
	}

	fmt.Println("================================")
}

// ---------
// helpers
// ---------

func shortHash(h [32]byte) string {
	return fmt.Sprintf("%x", h[:4]) // first 4 bytes only
}
