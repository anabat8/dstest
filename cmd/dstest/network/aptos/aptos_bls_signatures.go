package aptos

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fardream/go-bcs/bcs"
	blst "github.com/supranational/blst/bindings/go"
	"golang.org/x/crypto/sha3"
)

type (
	SK        = blst.SecretKey
	PK        = blst.P1Affine // G1, 48 bytes compressed, in minimal-pubkey-size
	Signature = blst.P2Affine // G2, 96 bytes compressed, in minimal-pubkey-size
)

// Aptos's domain separation tag (DST) for hashing a message before signing it
var DST = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")

// Signs the message with the given secret key, using DST for hashing, and returns the compressed signature bytes
// The returned signature should have length 96 bytes
func Sign(sk *SK, msg []byte) []byte {
	return new(Signature).Sign(sk, msg, DST).Compress()
}

// Verify checks that sig is a valid signature of msg under pk, using Aptos's POP DST
func Verify(sig *Signature, pk *PK, msg []byte) bool {
	return sig.Verify(true, pk, true, msg, DST)
}

func aggregate(sigs []*Signature) *Signature {
	agg := new(blst.P2Aggregate)
	agg.Aggregate(sigs, true)
	return agg.ToAffine()
}

// VerifyAggregate checks that sig is a valid BLS aggregate signature over msg,
// formed by aggregating individual signatures from the validators corresponding
// to pks (each having signed the same msg). Uses Aptos's POP DST
//
// "Fast" variant: every signer signed the same message (true for QC formation,
// where all 2f+1 contributors sign the same LedgerInfo).
func VerifyAggregate(sig *Signature, pks []*PK, msg []byte) bool {
	return sig.FastAggregateVerify(true, pks, msg, DST)
}

// Returns a 32-byte seed of the form:
// SHA3-256("APTOS::" + typeName),
// where typeName is in general the name of the struct being signed,
// e.g. seed(LedgerInfo) = SHA3-256("APTOS::LedgerInfo")
func Seed(typeName string) [32]byte {
	return sha3.Sum256([]byte("APTOS::" + typeName))
}

// A regular proposal (ProposalMsg) is signed on the full BlockData struct
// Builds the signing payload: seed("BlockData") || bcs(BlockData), then BLS signs it
func SignBlockData(sk *SK, bd *BlockData) (BLSSignature, error) {
	bdBytes, err := bcs.Marshal(bd)
	if err != nil {
		return nil, err
	}
	seed := Seed("BlockData")
	msg := append(seed[:], bdBytes...)
	return Sign(sk, msg), nil
}

// A VoteMsg is signed on the LedgerInfo inner struct of the Vote field
// Builds the signing payload: seed("LedgerInfo") || bcs(LedgerInfo), then BLS signs it
func SignLedgerInfo(sk *SK, li *LedgerInfo) (BLSSignature, error) {
	liBytes, err := bcs.Marshal(li)
	if err != nil {
		return nil, err
	}
	seed := Seed("LedgerInfo")
	msg := append(seed[:], liBytes...)
	return Sign(sk, msg), nil
}

type TimeoutRepr struct {
	Epoch    uint64
	Round    Round
	HQCRound Round
}

// A RoundTimeoutMsg is signed only on some fields of the inner TwoChainTimeout struct:
// epoch, round, qc.vote_data.proposed.round
func SignTimeoutRepr(sk *SK, epoch uint64, round, hqcRound Round) (BLSSignature, error) {
	timeoutRepr := TimeoutRepr{
		Epoch:    epoch,
		Round:    round,
		HQCRound: hqcRound,
	}
	timeoutBytes, err := bcs.Marshal(timeoutRepr)
	if err != nil {
		return nil, err
	}
	seed := Seed("TimeoutSigningRepr")
	msg := append(seed[:], timeoutBytes...)
	return Sign(sk, msg), nil
}

// Recomputes vote.LedgerInfo.ConsensusDataHash = vote_data.hash() for a mutated vote_data
// then signs the updated LedgerInfo with the voter's SK, and overwrites the new signature back
// This need to be called after mutating any field in vote.VoteData or vote.LedgerInfo.CommitInfo
// Mutations outside both (e.g., vote.TwoChainTimeout, or the surrounding VoteMsg.SyncInfo)
// don't change the signed payload and don't need a resign
func ResignVote(vote *Vote, sk *SK) error {
	// Recompute consensus_data_hash = SHA3-256(seed("VoteData") || bcs(VoteData))
	vdBytes, err := bcs.Marshal(vote.VoteData)
	if err != nil {
		return err
	}
	seedVD := Seed("VoteData")
	cdh := sha3.Sum256(append(seedVD[:], vdBytes...))
	vote.LedgerInfo.ConsensusDataHash = HashValue(cdh)

	// Sign the updated LedgerInfo
	sig, err := SignLedgerInfo(sk, &vote.LedgerInfo)
	if err != nil {
		return err
	}
	vote.Signature.Signature = sig
	return nil
}

// Computes the signature over the mutated BlockData of a proposal Block
// and overwrites the signature in-place in the proposal block
func ResignProposal(proposalBlock *Block, sk *SK) error {
	sig, err := SignBlockData(sk, &proposalBlock.BlockData)
	if err != nil {
		return err
	}
	proposalBlock.Signature = &OptionBLSSignature{Some: &sig}
	return nil
}

// Computes the signature over the mutated RoundTimeout of a RoundTimeoutMsg
// Writes back to RoundTimeout.Signature
func ResignRoundTimeout(rt *RoundTimeout, sk *SK) error {
	sig, err := SignTimeoutRepr(sk, rt.Timeout.Epoch, rt.Timeout.Round, rt.Timeout.QuorumCert.VoteData.Proposed.Round)
	if err != nil {
		return err
	}
	rt.Signature = sig
	return nil
}

// Computes the signature over the mutated LedgerInfo of a CommitVote
// Writes back to CommitVote.Signature
func ResignCommitVote(cv *CommitVote, sk *SK) error {
	sig, err := SignLedgerInfo(sk, &cv.LedgerInfo)
	if err != nil {
		return err
	}
	cv.Signature.Signature = sig
	return nil
}

// Computes the signature over a mutated field of a CommitMessage enum
// either CommitVote or CommitDecision
// Ack/Nack are unsigned, so no resign needed
func ResignCommitMessage(cm *CommitMessage, sk *SK) error {
	if cm.Vote != nil {
		return ResignCommitVote(cm.Vote, sk)
	}
	if cm.Decision != nil {
		return nil
	}
	return nil
}

func ResignQC(qc *QuorumCert, keysByAuthor map[AccountAddress]*SK, orderedAddrs []AccountAddress) error {
	return ResignAggregate(qc.VoteData, &qc.SignedLedgerInfo, keysByAuthor, orderedAddrs)
}

func ResignWrappedLedgerInfo(wli *WrappedLedgerInfo, keysByAuthor map[AccountAddress]*SK, orderedAddrs []AccountAddress) error {
	return ResignAggregate(wli.VoteData, &wli.SignedLedgerInfo, keysByAuthor, orderedAddrs)
}

// Computes the signature over the mutated VoteData of a QuorumCert/WrappedLedgerInfo
// To do so, we need to:
//   - update the consensus_data_hash in the LedgerInfo to match the new VoteData hash
//   - derive the list of contributors from the original bitmask,
//     and get their SKs from keysByAuthor
//   - bit i of the bitmask corresponds to validator at orderedAddrs[i]
//   - build the aggregate signature over the new LedgerInfo using those SKs
func ResignAggregate(vd VoteData, signedLI *LedgerInfoWithSignatures,
	keysByAuthor map[AccountAddress]*SK,
	orderedAddrs []AccountAddress) error {
	if signedLI.V0 == nil {
		return fmt.Errorf("SignedLedgerInfo has no V0")
	}

	vdBytes, err := bcs.Marshal(vd)
	if err != nil {
		return err
	}
	seed := Seed("VoteData")
	cdh := sha3.Sum256(append(seed[:], vdBytes...))
	signedLI.V0.LedgerInfo.ConsensusDataHash = HashValue(cdh)

	return reaggregateLedgerInfo(signedLI, keysByAuthor, orderedAddrs)
}

// Reaggregates the signatures of a CommitDecision over its
// new LedgerInfo, using the same set of contributors as the original
// bitmask
// We don't need to recompute CDH of LedgerInfo here since
// a CommitDecision does not have a VoteData field to match against.
func ResignCommitDecision(cd *CommitDecision,
	keysByAuthor map[AccountAddress]*SK,
	orderedAddrs []AccountAddress) error {
	if cd.LedgerInfo.V0 == nil {
		return fmt.Errorf("SignedLedgerInfo has no V0")
	}

	return reaggregateLedgerInfo(&cd.LedgerInfo, keysByAuthor, orderedAddrs)
}

// Rebuilds the AggregateSignature over the LedgerInfo bytes
// currently held in signedLI.V0.LedgerInfo, using the SKs of validators marked in
// the existing bitmask (bit i = orderedAddrs[i])
func reaggregateLedgerInfo(signedLI *LedgerInfoWithSignatures,
	keysByAuthor map[AccountAddress]*SK,
	orderedAddrs []AccountAddress) error {
	origMask := signedLI.V0.Signatures.ValidatorBitmask.Inner
	var contributorSKs []*SK
	for i := 0; i < len(orderedAddrs); i++ {
		if int(origMask[i/8])&(1<<(7-uint(i%8))) != 0 {
			contributorSKs = append(contributorSKs, keysByAuthor[orderedAddrs[i]])
		}
	}
	if len(contributorSKs) == 0 {
		return fmt.Errorf("Bitmask has no contributors")
	}

	// each contributor signs the (possibly mutated) LedgerInfo
	li := &signedLI.V0.LedgerInfo
	individuals := make([]*Signature, len(contributorSKs))
	for i, sk := range contributorSKs {
		sigBytes, err := SignLedgerInfo(sk, li)
		if err != nil {
			return err
		}
		individuals[i] = new(Signature).Uncompress(sigBytes)
		if individuals[i] == nil {
			return fmt.Errorf("uncompress sig %d failed", i)
		}
	}

	agg := aggregate(individuals)
	aggBytes := BLSSignature(agg.Compress())

	// write back; bitmask is unchanged because we used the same contributors
	signedLI.V0.Signatures = AggregateSignature{
		ValidatorBitmask: BitVec{Inner: origMask},
		Sig:              &OptionBLSSignature{Some: &aggBytes},
	}
	return nil
}

// Constructs a new LedgerInfoWithSignatures over the given LedgerInfo with every
// validator in orderedAddrs co-signing it (full-quorum aggregate). The resulting
// LedgerInfoWithSignatures is what a legitimate CommitDecision would carry once 2f+1
// validators agree on the commit; producing it here lets us fabricate a Decision from
// a single Vote (or any LedgerInfo) without waiting for real quorum
//
// Bitmask is all-1s for the first len(orderedAddrs) bits
func BuildFullQuorumLedgerInfoWithSignatures(li LedgerInfo,
	keysByAuthor map[AccountAddress]*SK,
	orderedAddrs []AccountAddress) (LedgerInfoWithSignatures, error) {
	n := len(orderedAddrs)
	if n == 0 {
		return LedgerInfoWithSignatures{}, fmt.Errorf("no validators in orderedAddrs")
	}

	bitmask := make([]byte, (n+7)/8)
	individuals := make([]*Signature, 0, n)
	for i := 0; i < n; i++ {
		sk := keysByAuthor[orderedAddrs[i]]
		if sk == nil {
			return LedgerInfoWithSignatures{}, fmt.Errorf("missing SK for validator %d", i)
		}
		sigBytes, err := SignLedgerInfo(sk, &li)
		if err != nil {
			return LedgerInfoWithSignatures{}, err
		}
		sig := new(Signature).Uncompress(sigBytes)
		if sig == nil {
			return LedgerInfoWithSignatures{}, fmt.Errorf("uncompress sig %d failed", i)
		}
		individuals = append(individuals, sig)
		bitmask[i/8] |= 1 << (7 - uint(i%8))
	}

	agg := aggregate(individuals)
	aggBytes := BLSSignature(agg.Compress())
	return LedgerInfoWithSignatures{
		V0: &LedgerInfoWithV0{
			LedgerInfo: li,
			Signatures: AggregateSignature{
				ValidatorBitmask: BitVec{Inner: bitmask},
				Sig:              &OptionBLSSignature{Some: &aggBytes},
			},
		},
	}, nil
}

// Helper functions for extracting SKs and loading the list of validators

// Walks ${BASE_DIR}/nodes/*/genesis/validator-identity.yaml
// (BASE_DIR defaults to /tmp/aptos-dstest) and returns:
//   - keysByAuthor: map of account_address -> BLS consensus secret key
//   - orderedAddrs: addresses in directory walk order (v0, v1, v2, ...)
//
// The orderedAddrs list is needed to map the bitmask in aggregated signatures back to the
// corresponding validators who signed.
// So bit i of the bitmask corresponds to orderedAddrs[i]
func CollectValidatorKeysByAuthor() (map[AccountAddress]*SK, []AccountAddress) {
	baseDir := os.Getenv("BASE_DIR")
	if baseDir == "" {
		baseDir = "/tmp/aptos-dstest"
	}

	keys := make(map[AccountAddress]*SK)
	var orderedAddrs []AccountAddress
	nodesDir := filepath.Join(baseDir, "nodes")

	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		return keys, orderedAddrs
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(nodesDir, entry.Name(), "genesis", "validator-identity.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		addrHex := extractYamlField(string(data), "account_address")
		skHex := extractYamlField(string(data), "consensus_private_key")
		if addrHex == "" || skHex == "" {
			continue
		}

		addrBytes, err := hex.DecodeString(strings.TrimPrefix(addrHex, "0x"))
		if err != nil || len(addrBytes) != 32 {
			continue
		}
		skBytes, err := hex.DecodeString(strings.TrimPrefix(skHex, "0x"))
		if err != nil {
			continue
		}
		sk := new(blst.SecretKey).Deserialize(skBytes)
		if sk == nil {
			continue
		}

		var addr AccountAddress
		copy(addr[:], addrBytes)
		keys[addr] = sk
		orderedAddrs = append(orderedAddrs, addr)
	}

	return keys, orderedAddrs
}

// Helper to extract the consensus_private_key field from the validator-identity.yaml
// Returns the trimmed value of the first "key: value" line found
// Strips surrounding quotes; returns "" if the key is not present
func extractYamlField(data, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			val = strings.Trim(val, "\"")
			return val
		}
	}
	return ""
}
