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

type OptionEpochState struct {
	None *BcsUnit
	Some *EpochState
}

func (OptionEpochState) IsBcsEnum() {}

type OptionBLSSignature struct {
	None *BcsUnit
	Some *BLSSignature
}

func (OptionBLSSignature) IsBcsEnum() {}

type OptionWrappedLedgerInfo struct {
	None *BcsUnit
	Some *WrappedLedgerInfo
}

func (OptionWrappedLedgerInfo) IsBcsEnum() {}

type OptionTwoChainTimeoutCertificate struct {
	None *BcsUnit
	Some *TwoChainTimeoutCertificate
}

func (OptionTwoChainTimeoutCertificate) IsBcsEnum() {}

type OptionUint64 struct {
	None *BcsUnit
	Some *uint64
}

func (OptionUint64) IsBcsEnum() {}

func DebugUnmarshal(logf func(string, ...any), name string, data []byte, v any) {
	n, err := bcs.Unmarshal(data, v)
	logf("%s: consumed=%d total=%d remaining=%d err=%v", name, n, len(data), len(data)-n, err)
}

// ------------
// ProposalMsg
// ------------
type ProposalMsg struct {
	Proposal Block
	SyncInfo SyncInfo
}

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

// ----------
// SyncInfo
// ----------

type SyncInfo struct {
	HighestQuorumCert        QuorumCert
	HighestOrderedCert       *OptionWrappedLedgerInfo
	HighestCommitCert        WrappedLedgerInfo
	Highest2ChainTimeoutCert *OptionTwoChainTimeoutCertificate
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

type WrappedLedgerInfo struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

// ----------
// Timeout certs
// ----------

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
