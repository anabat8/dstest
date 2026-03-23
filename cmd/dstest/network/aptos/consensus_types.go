package aptos

import (
	"fmt"
	"strings"

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

func DebugUnmarshal(logf func(string, ...any), name string, data []byte, v any) {
	n, err := bcs.Unmarshal(data, v)
	logf("%s: consumed=%d total=%d remaining=%d err=%v", name, n, len(data), len(data)-n, err)
}

// -------------------------------------------------------------------
type IConsensusMessage interface {
	GetRoundNumber() Round
	fmt.Stringer
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

func (m ProposalMsg) GetRoundNumber() Round {
	return m.Proposal.BlockData.Round
}

func (m ProposalMsg) String() string {
	return "ProposalMsg:\n" +
		indent(m.Proposal.String(), "  ") + "\n" +
		indent(m.SyncInfo.String(), "  ")
}

// ----------
// OptProposalMsg
// OptProposalMsg contains the optimistic proposal and sync info.
// ----------
type OptProposalMsg struct {
	BlockData OptBlockData
	SyncInfo  SyncInfo
}

func (m OptProposalMsg) GetRoundNumber() Round {
	return m.BlockData.Round
}

func (m OptProposalMsg) String() string {
	return "OptProposalMsg:\n" +
		indent(m.BlockData.String(), "    ") + "\n" +
		indent(m.SyncInfo.String(), "    ")
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

func (v VoteMsg) GetRoundNumber() Round {
	return v.Vote.VoteData.Proposed.Round
}

func (v VoteMsg) String() string {
	return "VoteMsg:\n" +
		indent(v.Vote.String(), "    ") + "\n" +
		indent(v.SyncInfo.String(), "    ")
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

func (c CommitVote) GetRoundNumber() Round {
	return c.LedgerInfo.CommitInfo.Round
}

func (c CommitVote) String() string {
	return "CommitVote:\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(c.Author)) +
		indent(c.LedgerInfo.String(), "    ") + "\n" +
		indent(c.Signature.String(), "    ")
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

func (c CommitMessage) GetRoundNumber() Round {
	if c.Vote != nil {
		return c.Vote.GetRoundNumber()
	}
	if c.Decision != nil {
		return c.Decision.LedgerInfo.V0.LedgerInfo.CommitInfo.Round
	}
	return 0
}

func (c CommitMessage) String() string {
	switch {
	case c.Vote != nil:
		return "CommitMessage:\n" +
			indent(c.Vote.String(), "    ")
	case c.Decision != nil:
		return "CommitMessage:\n" +
			indent(c.Decision.String(), "    ")
	case c.Ack != nil:
		return "CommitMessage:\n  Ack"
	case c.Nack != nil:
		return "CommitMessage:\n  Nack"
	default:
		return "CommitMessage: <empty>"
	}
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

func (r RoundTimeoutMsg) GetRoundNumber() Round {
	return r.RoundTimeout.Timeout.Round
}

func (r RoundTimeoutMsg) String() string {
	return "RoundTimeoutMsg:\n" +
		indent(r.RoundTimeout.String(), "    ") + "\n" +
		indent(r.SyncInfo.String(), "    ")
}

// ---------
// CommitDecision
// ---------
type CommitDecision struct {
	LedgerInfo LedgerInfoWithSignatures
}

func (c CommitDecision) String() string {
	return "CommitDecision:\n" +
		indent(c.LedgerInfo.String(), "    ")
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

func (r RoundTimeout) String() string {
	reason := "<nil>"
	if r.Reason != nil {
		reason = r.Reason.String()
	}

	return "RoundTimeout:\n" +
		"  Timeout:\n" + indent(r.Timeout.String(), "    ") + "\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(r.Author)) +
		fmt.Sprintf("  Reason: %s\n", reason) +
		fmt.Sprintf("  SignatureLen: %d", len(r.Signature))
}

type RoundTimeoutReason struct {
	Unknown             *BcsUnit
	ProposalNotReceived *BcsUnit
	PayloadUnavailable  *RoundTimeoutReasonPayloadUnavailable
	NoQC                *BcsUnit
}

func (RoundTimeoutReason) IsBcsEnum() {}

func (r RoundTimeoutReason) String() string {
	switch {
	case r.Unknown != nil:
		return "Unknown"
	case r.ProposalNotReceived != nil:
		return "ProposalNotReceived"
	case r.PayloadUnavailable != nil:
		return "PayloadUnavailable:\n" +
			fmt.Sprintf("  MissingAuthors: %x", r.PayloadUnavailable.MissingAuthors.Inner)
	case r.NoQC != nil:
		return "NoQC"
	default:
		return "UnknownReason"
	}
}

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

func (b Block) String() string {
	out := "Block:\n" +
		indent(b.BlockData.String(), "    ")

	if b.Signature != nil {
		out += "\n" + indent(b.Signature.String(), "    ")
	}

	return out
}

type BlockData struct {
	Epoch          uint64
	Round          Round
	TimestampUsecs uint64
	QuorumCert     QuorumCert
	BlockType      BlockType
}

func (b BlockData) String() string {
	return fmt.Sprintf(
		"BlockData:\n"+
			"  Epoch: %d\n"+
			"  Round: %d\n"+
			"  TimestampUsecs: %d\n"+
			"  QC info:\n%s\n"+
			"  BlockType:\n%s",
		b.Epoch,
		b.Round,
		b.TimestampUsecs,
		indent(b.QuorumCert.String(), "    "),
		indent(b.BlockType.String(), "    "),
	)
}

type OptionBLSSignature struct {
	None *BcsUnit
	Some *BLSSignature
}

func (OptionBLSSignature) IsBcsEnum() {}

func (o OptionBLSSignature) String() string {
	switch {
	case o.None != nil:
		return "None"
	case o.Some != nil:
		return fmt.Sprintf("Some(len=%d)", len(*o.Some))
	default:
		return "<invalid OptionBLSSignature>"
	}
}

// Same as BlockData, without QC and with parent id
type OptBlockData struct {
	Epoch          uint64
	Round          Round
	TimestampUsecs uint64
	Parent         BlockInfo
	BlockBody      *OptBlockBody
}

func (b OptBlockData) String() string {
	out := fmt.Sprintf(
		"OptBlockData:\n"+
			"  Epoch: %d\n"+
			"  Round: %d\n"+
			"  TimestampUsecs: %d\n"+
			"  Parent:\n%s",
		b.Epoch,
		b.Round,
		b.TimestampUsecs,
		indent(b.Parent.String(), "    "),
	)

	if b.BlockBody == nil {
		out += "\n  BlockBody: <nil>"
	} else {
		out += "\n  BlockBody:\n" + indent(b.BlockBody.String(), "    ")
	}

	return out
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

func (si SyncInfo) String() string {
	out := "SyncInfo:\n" +
		"  HighestQuorumCert:\n" + indent(si.HighestQuorumCert.String(), "    ") + "\n"

	if si.HighestOrderedCert == nil {
		out += "  HighestOrderedCert: <nil>\n"
	} else {
		out += "  HighestOrderedCert:\n" + indent(si.HighestOrderedCert.String(), "    ") + "\n"
	}

	out += "  HighestCommitCert:\n" + indent(si.HighestCommitCert.String(), "    ")

	if si.Highest2ChainTimeoutCert == nil {
		out += "\n  Highest2ChainTimeoutCert: <nil>"
	} else {
		out += "\n  Highest2ChainTimeoutCert:\n" + indent(si.Highest2ChainTimeoutCert.String(), "    ")
	}

	return out
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

func (v Vote) String() string {
	out := "Vote:\n" +
		indent(v.VoteData.String(), "    ") + "\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(v.Author)) +
		indent(v.LedgerInfo.String(), "    ") + "\n" +
		indent(v.Signature.String(), "    ")

	if v.TwoChainTimeout == nil {
		out += "\n  TwoChainTimeout: <nil>"
	} else {
		out += "\n" + indent(v.TwoChainTimeout.String(), "    ")
	}

	return out
}

// --------
// Signatures
// --------
type SignatureWithStatus struct {
	Signature BLSSignature
}

func (s SignatureWithStatus) String() string {
	return fmt.Sprintf("SignatureWithStatus:\n  SignatureLen: %d", len(s.Signature))
}

// ----------
// QC / vote data
// ----------

type QuorumCert struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

func (qc QuorumCert) String() string {
	return "QuorumCert:\n" +
		indent(qc.VoteData.String(), "    ") + "\n" +
		indent(qc.SignedLedgerInfo.String(), "    ")
}

type VoteData struct {
	Proposed BlockInfo
	Parent   BlockInfo
}

func (vd VoteData) String() string {
	return "VoteData:\n" +
		"  Proposed:\n" + indent(vd.Proposed.String(), "    ") + "\n" +
		"  Parent:\n" + indent(vd.Parent.String(), "    ")
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

func (bi BlockInfo) String() string {
	out := fmt.Sprintf(
		"BlockInfo:\n"+
			"  Epoch: %d\n"+
			"  Round: %d\n"+
			"  ID: %s\n"+
			"  ExecutedStateID: %s\n"+
			"  Version: %d\n"+
			"  TimestampUsecs: %d",
		bi.Epoch,
		bi.Round,
		shortHash(bi.ID),
		shortHash(bi.ExecutedStateID),
		bi.Version,
		bi.TimestampUsecs,
	)

	return out
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

func (l LedgerInfoWithSignatures) String() string {
	if l.V0 == nil {
		return "LedgerInfoWithSignatures: <nil>"
	}
	return indent(l.V0.String(), "  ")
}

func (LedgerInfoWithSignatures) IsBcsEnum() {}

type LedgerInfoWithV0 struct {
	LedgerInfo LedgerInfo
	Signatures AggregateSignature
}

func (l LedgerInfoWithV0) String() string {
	return "LedgerInfoWithV0:\n" +
		indent(l.LedgerInfo.String(), "    ") + "\n" +
		"  Signatures:\n" + indent(l.Signatures.String(), "    ")
}

type LedgerInfo struct {
	CommitInfo        BlockInfo
	ConsensusDataHash HashValue
}

func (l LedgerInfo) String() string {
	return "LedgerInfo:\n" +
		indent(l.CommitInfo.String(), "    ") + "\n" +
		fmt.Sprintf("  ConsensusDataHash: %s", shortHash(l.ConsensusDataHash))
}

type OptionWrappedLedgerInfo struct {
	None *BcsUnit
	Some *WrappedLedgerInfo
}

func (o OptionWrappedLedgerInfo) String() string {
	switch {
	case o.None != nil:
		return "None"
	case o.Some != nil:
		return o.Some.String()
	default:
		return "<invalid OptionWrappedLedgerInfo>"
	}
}

func (OptionWrappedLedgerInfo) IsBcsEnum() {}

type WrappedLedgerInfo struct {
	VoteData         VoteData
	SignedLedgerInfo LedgerInfoWithSignatures
}

func (w WrappedLedgerInfo) String() string {
	return "WrappedLedgerInfo:\n" +
		indent(w.VoteData.String(), "    ") + "\n" +
		indent(w.SignedLedgerInfo.String(), "    ")
}

// ----------
// Timeout certs
// ----------

type OptionTwoChainTimeoutWithSig struct {
	None *BcsUnit
	Some *TwoChainTimeoutWithSig
}

func (OptionTwoChainTimeoutWithSig) IsBcsEnum() {}

func (o OptionTwoChainTimeoutWithSig) String() string {
	switch {
	case o.None != nil:
		return "None"
	case o.Some != nil:
		return o.Some.String()
	default:
		return "<invalid OptionTwoChainTimeoutWithSig>"
	}
}

type TwoChainTimeoutWithSig struct {
	Timeout   TwoChainTimeout
	Signature BLSSignature
}

func (t TwoChainTimeoutWithSig) String() string {
	return "TwoChainTimeoutWithSig:\n" +
		indent(t.Timeout.String(), "    ") + "\n" +
		fmt.Sprintf("  SignatureLen: %d", len(t.Signature))
}

type OptionTwoChainTimeoutCertificate struct {
	None *BcsUnit
	Some *TwoChainTimeoutCertificate
}

func (o OptionTwoChainTimeoutCertificate) String() string {
	switch {
	case o.None != nil:
		return "None"
	case o.Some != nil:
		return o.Some.String()
	default:
		return "<invalid OptionTwoChainTimeoutWithSig>"
	}
}

func (OptionTwoChainTimeoutCertificate) IsBcsEnum() {}

type TwoChainTimeoutCertificate struct {
	Timeout              TwoChainTimeout
	SignaturesWithRounds AggregateSignatureWithRounds
}

func (t TwoChainTimeoutCertificate) String() string {
	return "TwoChainTimeoutCertificate:\n" +
		indent(t.Timeout.String(), "    ") + "\n" +
		indent(t.SignaturesWithRounds.String(), "    ")
}

type TwoChainTimeout struct {
	Epoch      uint64
	Round      Round
	QuorumCert QuorumCert
}

func (t TwoChainTimeout) String() string {
	return fmt.Sprintf(
		"TwoChainTimeout:\n"+
			"  Epoch: %d\n"+
			"  Round: %d\n"+
			"  %s",
		t.Epoch,
		t.Round,
		indent(t.QuorumCert.String(), "    "),
	)
}

type AggregateSignatureWithRounds struct {
	Sig    AggregateSignature
	Rounds []Round
}

func (a AggregateSignatureWithRounds) String() string {
	return fmt.Sprintf(
		"AggregateSignatureWithRounds:\n"+
			"  Rounds: %v\n"+
			"  %s",
		a.Rounds,
		indent(a.Sig.String(), "    "),
	)
}

type AggregateSignature struct {
	ValidatorBitmask BitVec
	Sig              *OptionBLSSignature
}

func (a AggregateSignature) String() string {
	sig := "<nil>"
	if a.Sig != nil {
		sig = a.Sig.String()
	}

	return "AggregateSignature:\n" +
		"  ValidatorBitmask:\n" + indent(a.ValidatorBitmask.String(), "    ") + "\n" +
		fmt.Sprintf("  Sig: %s", sig)
}

type BitVec struct {
	Inner []byte
}

func (b BitVec) String() string {
	return fmt.Sprintf("BitVec:\n  Bytes: %x\n  Len: %d", b.Inner, len(b.Inner))
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

func (b BlockType) String() string {
	switch {
	case b.Proposal != nil:
		return b.Proposal.String()
	case b.NilBlock != nil:
		return b.NilBlock.String()
	case b.Genesis != nil:
		return "Genesis"
	case b.ProposalExt != nil:
		return b.ProposalExt.String()
	case b.OptimisticProposal != nil:
		return b.OptimisticProposal.String()
	default:
		return "<unknown BlockType>"
	}
}

type BlockTypeProposal struct {
	Payload       Payload
	Author        Author
	FailedAuthors []RoundAuthorPair
}

func (b BlockTypeProposal) String() string {
	out := "BlockTypeProposal:\n" +
		"  Payload:\n" + indent(b.Payload.String(), "    ") + "\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(b.Author))

	if len(b.FailedAuthors) == 0 {
		out += "  FailedAuthors: []"
		return out
	}

	out += "  FailedAuthors:"
	for i, fa := range b.FailedAuthors {
		out += fmt.Sprintf("\n    [%d]:\n%s", i, indent(fa.String(), "      "))
	}
	return out
}

type BlockTypeNilBlock struct {
	FailedAuthors []RoundAuthorPair
}

func (b BlockTypeNilBlock) String() string {
	if len(b.FailedAuthors) == 0 {
		return "BlockTypeNilBlock:\n  FailedAuthors: []"
	}

	out := "BlockTypeNilBlock:\n  FailedAuthors:"
	for i, fa := range b.FailedAuthors {
		out += fmt.Sprintf("\n    [%d]:\n%s", i, indent(fa.String(), "      "))
	}
	return out
}

type RoundAuthorPair struct {
	Field0 Round
	Field1 Author
}

func (r RoundAuthorPair) String() string {
	return fmt.Sprintf(
		"RoundAuthorPair:\n"+
			"  Round: %d\n"+
			"  Author: %s",
		r.Field0,
		shortHash(r.Field1),
	)
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

func (p Payload) String() string {
	switch {
	case p.DirectMempool != nil:
		return fmt.Sprintf("DirectMempool:\n  Txns: %d", len(*p.DirectMempool))
	case p.InQuorumStore != nil:
		return "InQuorumStore"
	case p.InQuorumStoreWithLimit != nil:
		return "InQuorumStoreWithLimit"
	case p.QuorumStoreInlineHybrid != nil:
		return fmt.Sprintf("QuorumStoreInlineHybrid: %d batches\n",
			len(p.QuorumStoreInlineHybrid.Field0))
	case p.OptQuorumStore != nil:
		return "OptQuorumStore"
	case p.QuorumStoreInlineHybridV2 != nil:
		return fmt.Sprintf("QuorumStoreInlineHybridV2: %d batches\n",
			len(p.QuorumStoreInlineHybridV2.Field0))
	default:
		return "<unknown Payload>"
	}
}

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

func (p ProposalExt) String() string {
	if p.V0 == nil {
		return "ProposalExt: <nil>"
	}
	return p.V0.String()
}

type ProposalExtV0 struct {
	ValidatorTxns []ValidatorTransaction
	Payload       Payload
	Author        Author
	FailedAuthors []RoundAuthorPair
}

func (p ProposalExtV0) String() string {
	out := "ProposalExtV0:\n" +
		fmt.Sprintf("  ValidatorTxns: %d\n", len(p.ValidatorTxns)) +
		"  Payload:\n" + indent(p.Payload.String(), "    ") + "\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(p.Author))

	if len(p.FailedAuthors) == 0 {
		out += "  FailedAuthors: []"
		return out
	}

	out += "  FailedAuthors:"
	for i, fa := range p.FailedAuthors {
		out += fmt.Sprintf("\n    [%d]:\n%s", i, indent(fa.String(), "      "))
	}
	return out
}

// ----------
// OptBlockBody enum
// ----------

type OptBlockBody struct {
	V0 *OptBlockBodyV0
}

func (b OptBlockBody) String() string {
	if b.V0 == nil {
		return "OptBlockBody: <nil>"
	}
	return b.V0.String()
}

func (OptBlockBody) IsBcsEnum() {}

type OptBlockBodyV0 struct {
	ValidatorTxns []ValidatorTransaction
	Payload       Payload
	Author        Author
	GrandparentQC QuorumCert
}

func (b OptBlockBodyV0) String() string {
	return "OptBlockBodyV0:\n" +
		fmt.Sprintf("  ValidatorTxns: %d\n", len(b.ValidatorTxns)) +
		"  Payload:\n" + indent(b.Payload.String(), "    ") + "\n" +
		fmt.Sprintf("  Author: %s\n", shortHash(b.Author)) +
		"  GrandparentQC:\n" + indent(b.GrandparentQC.String(), "    ")
}

type SignedTransaction struct{}
type ProofWithData struct{}
type ProofWithDataWithTxnLimit struct{}
type OptQuorumStorePayload struct{}
type BatchInfo struct{}
type ValidatorTransaction struct{}

// ---------
// helpers
// ---------

func shortHash(h [32]byte) string {
	return fmt.Sprintf("%x", h[:4]) // first 4 bytes only
}

func indent(s, prefix string) string {
	if s == "" {
		return prefix
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
