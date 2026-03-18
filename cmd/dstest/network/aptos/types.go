package aptos

import (
	"bytes"
	"fmt"

	"github.com/fardream/go-bcs/bcs"
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
	Stream  any `bcs:"-"`
}

func (e MultiplexMessage) IsBcsEnum() {}

type NetworkMessage struct {
	Error         any
	RpcRequest    *RpcRequest
	RpcResponse   *RpcResponse
	DirectSendMsg *DirectSendMsg
}

func (e NetworkMessage) IsBcsEnum() {}

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

//	ProtocolId is a unique identifier associated with each Aptos application protocol.
//
// For example, if `protocol_id == ProtocolId::ConsensusRpcBcs`, then its corresponding
// inbound rpc request will be dispatched to consensus for handling.
type ProtocolId struct {
	ConsensusRpcBcs                  *uint8
	ConsensusDirectSendBcs           *uint8
	MempoolDirectSend                *uint8
	StateSyncDirectSend              *uint8
	DiscoveryDirectSend              *uint8
	HealthCheckerRpc                 *uint8
	ConsensusDirectSendJson          *uint8
	ConsensusRpcJson                 *uint8
	StorageServiceRpc                *uint8
	MempoolRpc                       *uint8
	PeerMonitoringServiceRpc         *uint8
	ConsensusRpcCompressed           *uint8
	ConsensusDirectSendCompressed    *uint8
	NetbenchDirectSend               *uint8
	NetbenchRpc                      *uint8
	DKGDirectSendCompressed          *uint8
	DKGDirectSendBcs                 *uint8
	DKGDirectSendJson                *uint8
	DKGRpcCompressed                 *uint8
	DKGRpcBcs                        *uint8
	DKGRpcJson                       *uint8
	JWKConsensusDirectSendCompressed *uint8
	JWKConsensusDirectSendBcs        *uint8
	JWKConsensusDirectSendJson       *uint8
	JWKConsensusRpcCompressed        *uint8
	JWKConsensusRpcBcs               *uint8
	JWKConsensusRpcJson              *uint8
	ConsensusObserver                *uint8
	ConsensusObserverRpc             *uint8
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

func (p ProtocolId) DecodeInto(payload []byte, msg *ConsensusMsg) error {
	encoding := p.GetEncodingType()
	switch encoding {
	case "Compressed":
		decompressed := make([]byte, 0)
		lzReader := lz4.NewReader(bytes.NewReader(payload[4:])) // skip the 4-byte uncompressed length prefix
		for {
			buf := make([]byte, 128)
			n, err := lzReader.Read(buf)
			if err != nil {
				return fmt.Errorf("error durring decompression: %w", err)
			}
			if n == 0 {
				break
			}
			decompressed = append(decompressed, buf[:n]...)
		}
		_, err2 := bcs.Unmarshal(decompressed, msg)
		if err2 != nil {
			return fmt.Errorf("bcs error after decompression: %w", err2)
		}
	case "BCS":
		_, err := bcs.Unmarshal(payload, msg)
		if err != nil {
			return fmt.Errorf("Failed to decode BCS payload for ProtocolID=%s: %w", p.String(), err)
		}
	case "JSON":
		return fmt.Errorf("JSON decoding not implemented yet for ProtocolID=%s", p.String())
	default:
		return fmt.Errorf("Unknown encoding type for ProtocolID=%s", p.String())
	}
	return nil
}

// ConsensusMsg is the network data type used by Aptos in the consensus protocol.
type ConsensusMsg struct {
	DeprecatedBlockRetrievalRequest any
	BlockRetrievalResponse          any
	EpochRetrievalRequest           any
	ProposalMsg                     any
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
