package aptos

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/fardream/go-bcs/bcs"
	blst "github.com/supranational/blst/bindings/go"
)

func TestSignatureProposalMsg(t *testing.T) {
	// BCS hex of a captured Block (the inner struct of ProposalMsg.Proposal) from an aptos run
	const blockHex = "01000000000000000200000000000000d2427042955006000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff75300000000000000000000000000000000000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff7530000000000000000000000000000000000000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff753000000000000000000000000000000000070f928cc7f20014ef56f3357357834ffc37545c40ee337e3d2b44b5a90b58853010000000000b09b9210f8a86716d52b9c25e06b243962fc343cc5ec2d2e06c6669e7466e53f0101000000000000007b52efce834c73691c680346945ee1015d3f2cd68585e7ff84046cf18bcbda8c0160b861dd6f87dcccfb58e914db4fbb0d953f524ea91c58268ea36675f584a610a4107d467f7de172b7b8783128f4f3e4e10bd4df11b3be1b62072e28aafbeb013ad2a0b973c69c990681cae0f1c3d2790751c930aeec0e67cb7c9164d19bc50ada"
	// 32-byte BLS consensus_private_key of the proposer, from ${BASE_DIR}/<i>/genesis/validator-identity.yaml
	const skHex = "71d62ba57c918563e3087c4881a72a0d1c554c8f2e1d9a3f7766160a2f3d5eac"

	if blockHex == "" || skHex == "" {
		t.Skip("populate blockHex and skHex from a real run")
	}

	// Decode hex inputs
	wireBytes, err := hex.DecodeString(blockHex)
	if err != nil {
		t.Fatalf("bad blockHex: %v", err)
	}
	skBytes, err := hex.DecodeString(skHex)
	if err != nil {
		t.Fatalf("bad skHex: %v", err)
	}

	// Parse the captured Block
	var block Block
	if _, err := bcs.Unmarshal(wireBytes, &block); err != nil {
		t.Fatalf("Unmarshal Block: %v", err)
	}

	// The signature is on the Block (Option<BLSSignature>); it must be Some
	if block.Signature == nil || block.Signature.Some == nil {
		t.Fatalf("captured Block has no signature (Block.Signature.Some == nil)")
	}
	originalSig := []byte(*block.Signature.Some)
	if len(originalSig) != 96 {
		t.Fatalf("expected 96-byte sig, got %d", len(originalSig))
	}

	// Re-sign with the proposer's secret key (SignBlockData handles BCS + seed + Sign)
	sk := new(blst.SecretKey).Deserialize(skBytes)
	if sk == nil {
		t.Fatalf("blst rejected SK (bad bytes or wrong endianness)")
	}
	computedSig, err := SignBlockData(sk, &block.BlockData)
	if err != nil {
		t.Fatalf("SignBlockData: %v", err)
	}
	if len(computedSig) != 96 {
		t.Fatalf("expected 96-byte computed sig, got %d", len(computedSig))
	}

	if !bytes.Equal(computedSig, originalSig) {
		t.Fatalf("signature mismatch\n captured: %x\n computed: %x", originalSig, computedSig)
	}
}

func TestSignatureVoteMsg(t *testing.T) {
	// BCS hex of a captured Vote (inner struct, not VoteMsg) from an aptos run
	const voteHex = "010000000000000001000000000000009edba1ff74267275cb3bc2cb2f495019f86828552c0ac1356e5bd83ba973e681414343554d554c41544f525f504c414345484f4c4445525f48415348000000000000000000000000e71649cc89500600000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff75300000000000000000000000000000000007b52efce834c73691c680346945ee1015d3f2cd68585e7ff84046cf18bcbda8c0100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff7530000000000000000000000000000000000230f286a0868478cb62be380f2cd0ca1e12cda1be4d68ff754033360e931e65a60b71ac6f2501aca8e6c38483098adda5c4f00a29df452cf360ee55a0ce8d7ac8cc5c48286ecfdba4b896f4d1cb900046f11345955632f6b208b7f04e13e372b20a992ab38003e17642dddc9a8d9165ab7970d6871dd547a920afdf7f6b2d182a600"
	// 32-byte BLS consensus_private_key of the voter, from ${BASE_DIR}/<i>/genesis/validator-identity.yaml
	const skHex = "2febeba0933c822a185c94728875b6cbf3bd3528b0d2571ce85a1abc1b39b278"

	if voteHex == "" || skHex == "" {
		t.Skip("populate voteHex and skHex from a real run")
	}

	// Decode hex inputs
	wireBytes, err := hex.DecodeString(voteHex)
	if err != nil {
		t.Fatalf("bad voteHex: %v", err)
	}
	skBytes, err := hex.DecodeString(skHex)
	if err != nil {
		t.Fatalf("bad skHex: %v", err)
	}

	// Parse the captured Vote
	var vote Vote
	if _, err := bcs.Unmarshal(wireBytes, &vote); err != nil {
		t.Fatalf("Unmarshal Vote: %v", err)
	}

	originalSig := []byte(vote.Signature.Signature)
	if len(originalSig) != 96 {
		t.Fatalf("expected 96-byte sig, got %d", len(originalSig))
	}
	originalCDH := vote.LedgerInfo.ConsensusDataHash

	// Re-sign with the voter's secret key (ResignVote handles recomputing the CDH + Sign)
	sk := new(blst.SecretKey).Deserialize(skBytes)
	if sk == nil {
		t.Fatalf("blst rejected SK (bad bytes or wrong endianness)")
	}
	if err := ResignVote(&vote, sk); err != nil {
		t.Fatalf("ResignVote: %v", err)
	}

	// ConsensusDataHash should be unchanged if BCS(VoteData) and Seed("VoteData") are not mutated
	// In this test we want to ensure that the way we are computing CDH is correct
	// so there are no mutations involved.
	if vote.LedgerInfo.ConsensusDataHash != originalCDH {
		t.Fatalf("ConsensusDataHash mismatch: BCS(VoteData) or Seed(\"VoteData\") is wrong\n stored:     %x\n recomputed: %x",
			originalCDH[:], vote.LedgerInfo.ConsensusDataHash[:])
	}

	// ResignVote overwrote the signature in-place
	computedSig := []byte(vote.Signature.Signature)
	if !bytes.Equal(computedSig, originalSig) {
		t.Fatalf("signature mismatch\n captured: %x\n computed: %x", originalSig, computedSig)
	}
}

func TestSignatureRoundTimeoutMsg(t *testing.T) {
	// BCS hex of a captured RoundTimeout (innter struct of a RoundTimeoutMsg) from an aptos run
	const roundTimeoutHex = "010000000000000001000000000000000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff75300000000000000000000000000000000000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff7530000000000000000000000000000000000000100000000000000000000000000000027e7bc082d274d9e61a4da1bf78e991e6e267e6dcccfb0b327d6f712b5d4ad5971713455d1735d3b27d06c5971f710cbdc0bde95a4639d160b3a618bc21ff753000000000000000000000000000000000070f928cc7f20014ef56f3357357834ffc37545c40ee337e3d2b44b5a90b58853010000f8db7f17f3745934e2e5905054b32bb4fe0952b3c0d880395551c87f463eaaa80160b4d270060aaff85dc8321594a0c069abc934a633ad63a6071e2cf745bfb9ac104a5b1d55dd31f2ebc62f2473112d8cc9125760e21e2690c593cee1f3e0883796e53232f623fe2461ee6824995661b824a7cf4c55fec77e17e4274dcabc2cc277"
	// 32-byte BLS consensus_private_key of the proposer, from ${BASE_DIR}/<i>/genesis/validator-identity.yaml
	const skHex = "5395e8e1fe8eb8f1448523b908c4bacff7a7343ccd737152b27e1f618fc11314"

	if roundTimeoutHex == "" || skHex == "" {
		t.Skip("populate roundTimeoutHex and skHex from a real run")
	}

	// Decode hex inputs
	wireBytes, err := hex.DecodeString(roundTimeoutHex)
	if err != nil {
		t.Fatalf("bad blockHex: %v", err)
	}
	skBytes, err := hex.DecodeString(skHex)
	if err != nil {
		t.Fatalf("bad skHex: %v", err)
	}

	// Parse the captured RoundTimeout
	var rt RoundTimeout
	if _, err := bcs.Unmarshal(wireBytes, &rt); err != nil {
		t.Fatalf("Unmarshal Block: %v", err)
	}

	originalSig := rt.Signature
	if len(originalSig) != 96 {
		t.Fatalf("expected 96-byte sig, got %d", len(originalSig))
	}

	// Re-sign with the proposer's secret key (SignTimeoutRepr handles BCS + seed + Sign)
	sk := new(blst.SecretKey).Deserialize(skBytes)
	if sk == nil {
		t.Fatalf("blst rejected SK (bad bytes or wrong endianness)")
	}
	computedSig, err := SignTimeoutRepr(sk, rt.Timeout.Epoch, rt.Timeout.Round, rt.Timeout.QuorumCert.VoteData.Proposed.Round)
	if err != nil {
		t.Fatalf("SignTimeoutRepr: %v", err)
	}
	if len(computedSig) != 96 {
		t.Fatalf("expected 96-byte computed sig, got %d", len(computedSig))
	}

	if !bytes.Equal(computedSig, originalSig) {
		t.Fatalf("signature mismatch\n captured: %x\n computed: %x", originalSig, computedSig)
	}
}

func TestVoteWithAttachedTimeout(t *testing.T) {
	const voteHex = "01000000000000000100000000000000fcc29aea6d6b669b7af5dde2a45a9a01c4ee33f8a6153be266f14512454edea2414343554d554c41544f525f504c414345484f4c4445525f48415348000000000000000000000000c43bb7f7bf500600000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa0000000000000000000000000000000000a835aa6999947cc8c1ebd4f78b89eade025aa217e6e2db84fe51999fc781018c0100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa000000000000000000000000000000000079338983bedd11e367ef8896b55cb62103f39275e92b64cb5b8e82e4534a537b60939a103984168004e1a03202af8c70c4ae7e4dbe1f0b557b040718a65816b024efbce5e95e4c6aa04dd7edd98ea3a94c18f905ce79d805e09be2f348fd4cbb54ad33bd17405601f7e8c83224da0ce41f26a1b3f1cc3b31ff4d661f6798cb5130000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa00000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa0000000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa000000000000000000000000000000000005b93fd91f9997692b4137af0f2757fcec709e906e8ab27629abbc0b252929d2010000010100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa00000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa0000000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa000000000000000000000000000000000005b93fd91f9997692b4137af0f2757fcec709e906e8ab27629abbc0b252929d20100000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa00000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa0000000000000000000000000000000000000100000000000000000000000000000030ade2d122b7cdb56f9b7725670680ec3601fdde3d9111280e3abe243dba7ab0ea731401fe6ad901ea2121388534e535c2ebbb6ca4f842245a2ef0e0b719bdfa000000000000000000000000000000000005b93fd91f9997692b4137af0f2757fcec709e906e8ab27629abbc0b252929d201000000"
	const skHex = "2c001b0e7fe3a0b17829f4446ec3710e2b622126f6bd287cf3abb1e732e67f4b"

	if voteHex == "" || skHex == "" {
		t.Skip("no values for vote / sk")
	}

	wireBytes, _ := hex.DecodeString(voteHex)
	skBytes, _ := hex.DecodeString(skHex)

	var vote VoteMsg
	if _, err := bcs.Unmarshal(wireBytes, &vote); err != nil {
		t.Fatalf("bad voteHex")
	}

	sk := new(blst.SecretKey).Deserialize(skBytes)
	if sk == nil {
		t.Fatalf("blst rejected SK")
	}

	origVoteSig := append([]byte{}, vote.Vote.Signature.Signature...)

	epoch := vote.Vote.VoteData.Proposed.Epoch
	round := vote.Vote.VoteData.Proposed.Round
	hqcRound := vote.SyncInfo.HighestQuorumCert.VoteData.Proposed.Round

	timeoutSig, err := SignTimeoutRepr(sk, epoch, round, hqcRound)
	if err != nil {
		t.Fatalf("SignTimeoutRepr: %v", err)
	}

	vote.Vote.TwoChainTimeout = &OptionTwoChainTimeoutWithSig{
		Some: &TwoChainTimeoutWithSig{
			Timeout: TwoChainTimeout{
				Epoch:      epoch,
				Round:      round,
				QuorumCert: vote.SyncInfo.HighestQuorumCert,
			},
			Signature: timeoutSig,
		},
	}

	encoded, err := bcs.Marshal(vote.Vote)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !bytes.Equal(vote.Vote.Signature.Signature, origVoteSig) {
		t.Fatalf("Outer vote signature has changed after attaching timeout")
	}

	var roundTripped Vote
	if _, err := bcs.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("Unmarshal mutated vote: %v", err)
	}
	reencoded, _ := bcs.Marshal(roundTripped)
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("BCS round-trip mismatch")
	}

	pk := new(PK).From(sk)
	repr := TimeoutRepr{Epoch: epoch, Round: round, HQCRound: hqcRound}
	reprBytes, _ := bcs.Marshal(repr)
	seed := Seed("TimeoutSigningRepr")
	payload := append(seed[:], reprBytes...)

	innerSig := new(Signature).Uncompress(roundTripped.TwoChainTimeout.Some.Signature)
	if innerSig == nil {
		t.Fatalf("inner timeout sig invalid (Uncompress failed)")
	}
	if !innerSig.Verify(true, pk, true, payload, DST) {
		t.Fatalf("inner timeout sig does not verify under voter's PK")
	}
}

func TestQCWithMutatedVoteData(t *testing.T) {
	// BCS hex of a captured Block whose QC carries a real aggregate signature
	const blockHex = "020000000000000005000000000000007217a080fc5006000200000000000000040000000000000006dcf394dfbccbfebc8d2910a3b609a7794e759c26110e231282ceef342f832f414343554d554c41544f525f504c414345484f4c4445525f4841534800000000000000000000000012c05680fc5006000002000000000000000300000000000000d14dfc4531a9b390d96989cf60978ddc5e5bbe083153fd88042ae76f117256ad414343554d554c41544f525f504c414345484f4c4445525f4841534800000000000000000000000058d44180fc500600000002000000000000000300000000000000d14dfc4531a9b390d96989cf60978ddc5e5bbe083153fd88042ae76f117256ad414343554d554c41544f525f504c414345484f4c4445525f4841534800000000000000000000000058d44180fc50060000f765ff2b011674eb5886ce3f397399476b004854bc3fe117dde564aad2095be301b00160a68174a027c6e28498992b6455b2ef1c054b5790ea790b9f13974627e5242c6e0fff4407311242bb5ceb75d67e60b08302fe75d115b5db6d497b58ddb73dc84d88ec2a581222bab2471706961ca27a069b04fbc0194652f4206ffcfe54d15dcf000000a835aa6999947cc8c1ebd4f78b89eade025aa217e6e2db84fe51999fc781018c000160ab7cdbd3afddfa6a636733ac3dabdc71f3b3b1c0920e9f8b642773412319ea6310dcd7864e857fe5c5fa90a2562dc2c30e17fa2b1884f865727ce5922ded02fa04089a47b45ef0449d49a6dc7676b007caf509d7fc031bf69cc5ac86dc8fbde9"

	if blockHex == "" {
		t.Skip("populate blockHex from a real run")
	}

	keysByAuthor, orderedAddrs := CollectValidatorKeysByAuthor()
	if len(keysByAuthor) == 0 {
		t.Skip("no validator keys loaded; ensure ${BASE_DIR}/nodes/* is populated")
	}

	wireBytes, err := hex.DecodeString(blockHex)
	if err != nil {
		t.Fatalf("bad blockHex: %v", err)
	}

	var block Block
	if _, err := bcs.Unmarshal(wireBytes, &block); err != nil {
		t.Fatalf("Unmarshal Block: %v", err)
	}
	if block.BlockData.QuorumCert.SignedLedgerInfo.V0 == nil {
		t.Fatalf("captured Block has no V0 in QC's SignedLedgerInfo")
	}
	if block.BlockData.QuorumCert.SignedLedgerInfo.V0.Signatures.Sig == nil ||
		block.BlockData.QuorumCert.SignedLedgerInfo.V0.Signatures.Sig.Some == nil {
		t.Skip("captured QC has no aggregate signature (genesis/placeholder QC) — capture a Block from a later round (5+)")
	}

	origAgg := append([]byte{}, *block.BlockData.QuorumCert.SignedLedgerInfo.V0.Signatures.Sig.Some...)
	origCDH := block.BlockData.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.ConsensusDataHash

	// BLS aggregation is deterministic, so re-aggregating the same contributors over
	// the same LedgerInfo must produce the exact same 96 bytes of aggregate signature
	if err := ResignQC(&block.BlockData.QuorumCert, keysByAuthor, orderedAddrs); err != nil {
		t.Fatalf("ResignQC (no mutation): %v", err)
	}
	if block.BlockData.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.ConsensusDataHash != origCDH {
		t.Fatalf("CDH changed without mutation\n stored:     %x\n recomputed: %x",
			origCDH[:], block.BlockData.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.ConsensusDataHash[:])
	}
	newAgg := *block.BlockData.QuorumCert.SignedLedgerInfo.V0.Signatures.Sig.Some
	if !bytes.Equal(newAgg, origAgg) {
		t.Fatalf("ResignQC didn't reproduce original aggregate\n captured: %x\n computed: %x",
			origAgg, newAgg)
	}

	// Mutate VoteData, ResignQC, verify the new aggregate is valid
	// Joint shift to keep Proposed.Round > Parent.Round
	block.BlockData.QuorumCert.VoteData.Proposed.Round++
	block.BlockData.QuorumCert.VoteData.Parent.Round++

	if err := ResignQC(&block.BlockData.QuorumCert, keysByAuthor, orderedAddrs); err != nil {
		t.Fatalf("ResignQC (with mutation): %v", err)
	}

	// CDH must have changed because VoteData changed
	mutatedCDH := block.BlockData.QuorumCert.SignedLedgerInfo.V0.LedgerInfo.ConsensusDataHash
	if mutatedCDH == origCDH {
		t.Fatalf("CDH didn't change after mutation")
	}
	// Aggregate must have changed because the signed LedgerInfo changed
	mutatedAgg := *block.BlockData.QuorumCert.SignedLedgerInfo.V0.Signatures.Sig.Some
	if bytes.Equal(mutatedAgg, origAgg) {
		t.Fatalf("aggregate sig didn't change after mutation; aggregation didn't pick up the new LedgerInfo")
	}

	// Reproduce the receiver's aggregate-sig verification: build the same payload
	// and resolve the contributor PKs from the bitmask.
	qc := block.BlockData.QuorumCert
	li := qc.SignedLedgerInfo.V0.LedgerInfo
	liBytes, err := bcs.Marshal(li)
	if err != nil {
		t.Fatalf("Marshal LedgerInfo: %v", err)
	}
	seed := Seed("LedgerInfo")
	payload := append(seed[:], liBytes...)

	bitmask := qc.SignedLedgerInfo.V0.Signatures.ValidatorBitmask.Inner
	var contributorPKs []*PK
	for i := 0; i < len(orderedAddrs); i++ {
		if int(bitmask[i/8])&(1<<(7-uint(i%8))) != 0 {
			contributorPKs = append(contributorPKs, new(PK).From(keysByAuthor[orderedAddrs[i]]))
		}
	}
	if len(contributorPKs) == 0 {
		t.Fatalf("bitmask has no contributors after mutation")
	}

	aggSig := new(Signature).Uncompress(mutatedAgg)
	if aggSig == nil {
		t.Fatalf("uncompress mutated aggregate failed")
	}
	if !VerifyAggregate(aggSig, contributorPKs, payload) {
		t.Fatalf("mutated aggregate signature does not verify under contributor PKs")
	}
}
