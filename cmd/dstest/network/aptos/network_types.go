package aptos

import (
	"encoding/binary"
	"fmt"

	"github.com/aptos-labs/aptos-go-sdk/bcs"
	"github.com/pierrec/lz4/v4"
)

// AptosNetworkEnvelope represents the decoded contents of an aptos Noise stream frame after a successful decryption
type AptosNetworkEnvelope struct {
	Variant    string // "DirectSendMsg", "RpcRequest", "RpcResponse"
	ProtocolID string // "ConsensusRpcCompressed", etc.
	Payload    []byte
}

type MultiplexMessage struct {
	Message *NetworkMessage
	Stream  *StreamMessage
}

func (e MultiplexMessage) IsBcsEnum() {}

type StreamMessage struct {
}

type NetworkMessage struct {
	Error         *ErrorCode
	RpcRequest    *RpcRequest
	RpcResponse   *RpcResponse
	DirectSendMsg *DirectSendMsg
}

func (e NetworkMessage) IsBcsEnum() {}

type ErrorCode struct {
}

type DirectSendMsg struct {
	ProtocolID *ProtocolId
	Priority   *uint8
	RawMsg     []byte
}

type RpcRequest struct {
	ProtocolID *ProtocolId
	RequestID  *uint32
	Priority   *uint8
	RawRequest []byte
}

type RpcResponse struct {
	RequestID   *uint32
	Priority    *uint8
	RawResponse []byte
}

type BcsUnit struct{}

//	ProtocolId is a unique identifier associated with each Aptos application protocol.
//
// For example, if `protocol_id == ProtocolId::ConsensusRpcBcs`, then its corresponding
// inbound rpc request will be dispatched to consensus for handling.
type ProtocolId struct {
	ConsensusRpcBcs                  *BcsUnit
	ConsensusDirectSendBcs           *BcsUnit
	MempoolDirectSend                *BcsUnit
	StateSyncDirectSend              *BcsUnit
	DiscoveryDirectSend              *BcsUnit
	HealthCheckerRpc                 *BcsUnit
	ConsensusDirectSendJson          *BcsUnit
	ConsensusRpcJson                 *BcsUnit
	StorageServiceRpc                *BcsUnit
	MempoolRpc                       *BcsUnit
	PeerMonitoringServiceRpc         *BcsUnit
	ConsensusRpcCompressed           *BcsUnit
	ConsensusDirectSendCompressed    *BcsUnit
	NetbenchDirectSend               *BcsUnit
	NetbenchRpc                      *BcsUnit
	DKGDirectSendCompressed          *BcsUnit
	DKGDirectSendBcs                 *BcsUnit
	DKGDirectSendJson                *BcsUnit
	DKGRpcCompressed                 *BcsUnit
	DKGRpcBcs                        *BcsUnit
	DKGRpcJson                       *BcsUnit
	JWKConsensusDirectSendCompressed *BcsUnit
	JWKConsensusDirectSendBcs        *BcsUnit
	JWKConsensusDirectSendJson       *BcsUnit
	JWKConsensusRpcCompressed        *BcsUnit
	JWKConsensusRpcBcs               *BcsUnit
	JWKConsensusRpcJson              *BcsUnit
	ConsensusObserver                *BcsUnit
	ConsensusObserverRpc             *BcsUnit
}

func (e ProtocolId) IsBcsEnum() {}

func (p ProtocolId) String() string {
	switch {
	case p.ConsensusRpcBcs != nil:
		return "ConsensusRpcBcs"
	case p.ConsensusDirectSendBcs != nil:
		return "ConsensusDirectSendBcs"
	case p.MempoolDirectSend != nil:
		return "MempoolDirectSend"
	case p.StateSyncDirectSend != nil:
		return "StateSyncDirectSend"
	case p.DiscoveryDirectSend != nil:
		return "DiscoveryDirectSend"
	case p.HealthCheckerRpc != nil:
		return "HealthCheckerRpc"
	case p.ConsensusDirectSendJson != nil:
		return "ConsensusDirectSendJson"
	case p.ConsensusRpcJson != nil:
		return "ConsensusRpcJson"
	case p.StorageServiceRpc != nil:
		return "StorageServiceRpc"
	case p.MempoolRpc != nil:
		return "MempoolRpc"
	case p.PeerMonitoringServiceRpc != nil:
		return "PeerMonitoringServiceRpc"
	case p.ConsensusRpcCompressed != nil:
		return "ConsensusRpcCompressed"
	case p.ConsensusDirectSendCompressed != nil:
		return "ConsensusDirectSendCompressed"
	case p.NetbenchDirectSend != nil:
		return "NetbenchDirectSend"
	case p.NetbenchRpc != nil:
		return "NetbenchRpc"
	case p.DKGDirectSendCompressed != nil:
		return "DKGDirectSendCompressed"
	case p.DKGDirectSendBcs != nil:
		return "DKGDirectSendBcs"
	case p.DKGDirectSendJson != nil:
		return "DKGDirectSendJson"
	case p.DKGRpcCompressed != nil:
		return "DKGRpcCompressed"
	case p.DKGRpcBcs != nil:
		return "DKGRpcBcs"
	case p.DKGRpcJson != nil:
		return "DKGRpcJson"
	case p.JWKConsensusDirectSendCompressed != nil:
		return "JWKConsensusDirectSendCompressed"
	case p.JWKConsensusDirectSendBcs != nil:
		return "JWKConsensusDirectSendBcs"
	case p.JWKConsensusDirectSendJson != nil:
		return "JWKConsensusDirectSendJson"
	case p.JWKConsensusRpcCompressed != nil:
		return "JWKConsensusRpcCompressed"
	case p.JWKConsensusRpcBcs != nil:
		return "JWKConsensusRpcBcs"
	case p.JWKConsensusRpcJson != nil:
		return "JWKConsensusRpcJson"
	case p.ConsensusObserver != nil:
		return "ConsensusObserver"
	case p.ConsensusObserverRpc != nil:
		return "ConsensusObserverRpc"
	default:
		return fmt.Sprintf("UnknownProtocolId(%v)", p)
	}
}

func (p ProtocolId) IsConsensus() bool {
	return p.ConsensusRpcBcs != nil ||
		p.ConsensusRpcJson != nil ||
		p.ConsensusRpcCompressed != nil ||
		p.ConsensusDirectSendBcs != nil ||
		p.ConsensusDirectSendJson != nil ||
		p.ConsensusDirectSendCompressed != nil
}

// Based on ProtocolId, we know a message's serialization: BCS, JSON, or compressed.
func (p ProtocolId) GetEncodingType() string {
	switch {
	case p.ConsensusRpcCompressed != nil || p.ConsensusDirectSendCompressed != nil:
		return "Compressed"
	case p.ConsensusRpcBcs != nil || p.ConsensusDirectSendBcs != nil:
		return "BCS"
	case p.ConsensusRpcJson != nil || p.ConsensusDirectSendJson != nil:
		return "JSON"
	}
	return "UnknownEncodingType"
}

func (p ProtocolId) DecodeConsensusPayload(payload []byte) ([]byte, uint32, int, error) {
	encoding := p.GetEncodingType()
	var decoded []byte

	switch encoding {
	case "Compressed":
		// Decompress the payload using lz4 and then decode the inner BCS message.
		if len(payload) < 4 {
			return nil, 0, 0, fmt.Errorf("Compressed payload too short to contain uncompressed length prefix for ProtocolID=%s", p.String())
		}

		uncompressedSize := int(binary.LittleEndian.Uint32(payload[:4]))
		dst := make([]byte, uncompressedSize)

		n, err := lz4.UncompressBlock(payload[4:], dst)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("lz4 block decompression failed for %s: %w", p.String(), err)
		}
		if n != uncompressedSize {
			return nil, 0, 0, fmt.Errorf("lz4 block decompressed unexpected size for %s: got=%d want=%d", p.String(), n, uncompressedSize)
		}

		decoded = dst

	case "BCS":
		decoded = payload

	case "JSON":
		return nil, 0, 0, fmt.Errorf("JSON decoding not implemented yet for ProtocolID=%s", p.String())
	default:
		return nil, 0, 0, fmt.Errorf("Unknown encoding type for ProtocolID=%s", p.String())
	}

	// For consensus messages, the first field is always a uleb128 enum tag indicating the message variant.
	// We want to read this tag to determine if we will further decode this payload.
	des := bcs.NewDeserializer(decoded)
	val_des := des.Uleb128()
	tag_bytes_consumed := len(decoded) - des.Remaining()

	return decoded, val_des, tag_bytes_consumed, nil

	//variant, n, err := readULEB128(decoded)
	// if err != nil {
	// 	return nil, 0, 0, fmt.Errorf("failed reading consensus enum tag for ProtocolID=%s: %w", p.String(), err)
	// }

	// return decoded, variant, n, nil
}

// ConsensusMsg is the network data type used by Aptos in the consensus protocol.
type ConsensusMsg struct {
	DeprecatedBlockRetrievalRequest any
	BlockRetrievalResponse          any
	EpochRetrievalRequest           any
	ProposalMsg                     *ProposalMsg
	SyncInfo                        any
	EpochChangeProof                any
	VoteMsg                         any
	CommitVoteMsg                   any
	CommitDecisionMsg               any
	BatchMsg                        any
	BatchRequestMsg                 any
	BatchResponse                   any
	SignedBatchInfo                 any
	ProofOfStoreMsg                 any
	DAGMessage                      any
	CommitMessage                   any
	RandGenMessage                  any
	BatchResponseV2                 any
	OrderVoteMsg                    any
	RoundTimeoutMsg                 any
	BlockRetrievalRequest           any
	OptProposalMsg                  any
	BatchMsgV2                      any
	SignedBatchInfoMsgV2            any
	ProofOfStoreMsgV2               any
	SecretShareMsg                  any
}

func (ConsensusMsg) IsBcsEnum() {}

// Helper for reading a uleb128-encoded unsigned integer from the start of a byte slice.
// Returns the decoded integer, the number of bytes consumed, and an error if decoding fails.
func ReadULEB128(b []byte) (uint64, int, error) {
	var result uint64
	var shift uint
	for i, by := range b {
		result |= uint64(by&0x7f) << shift
		if by&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, 0, fmt.Errorf("uleb128 too large")
		}
	}
	return 0, 0, fmt.Errorf("incomplete uleb128")
}

func ConsensusMsgVariantName(idx uint32) string {
	switch idx {
	case 0:
		return "DeprecatedBlockRetrievalRequest"
	case 1:
		return "BlockRetrievalResponse"
	case 2:
		return "EpochRetrievalRequest"
	case 3:
		return "ProposalMsg"
	case 4:
		return "SyncInfo"
	case 5:
		return "EpochChangeProof"
	case 6:
		return "VoteMsg"
	case 7:
		return "CommitVoteMsg"
	case 8:
		return "CommitDecisionMsg"
	case 9:
		return "BatchMsg"
	case 10:
		return "BatchRequestMsg"
	case 11:
		return "BatchResponse"
	case 12:
		return "SignedBatchInfo"
	case 13:
		return "ProofOfStoreMsg"
	case 14:
		return "DAGMessage"
	case 15:
		return "CommitMessage"
	case 16:
		return "RandGenMessage"
	case 17:
		return "BatchResponseV2"
	case 18:
		return "OrderVoteMsg"
	case 19:
		return "RoundTimeoutMsg"
	case 20:
		return "BlockRetrievalRequest"
	case 21:
		return "OptProposalMsg"
	case 22:
		return "BatchMsgV2"
	case 23:
		return "SignedBatchInfoMsgV2"
	case 24:
		return "ProofOfStoreMsgV2"
	case 25:
		return "SecretShareMsg"
	default:
		return fmt.Sprintf("UnknownConsensusMsg(%d)", idx)
	}
}
