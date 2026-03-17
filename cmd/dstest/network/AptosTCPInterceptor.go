package network

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"sync"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"github.com/fardream/go-bcs/bcs"
	"github.com/pierrec/lz4/v4"
)

type AptosTCPInterceptor struct {
	BaseInterceptor
	Listener net.Listener
	keyReg   *aptos.KeyRegistry // For keeping track of all sessions keys
}

// Check if BaseInterceptor implements Interceptor interface
var _ Interceptor = (*AptosTCPInterceptor)(nil)

func (ni *AptosTCPInterceptor) Init(id int, port int, nm *Manager) {
	logPrefix := fmt.Sprintf("[AptosTCP Interceptor %d] ", id)
	logger := log.New(log.Writer(), logPrefix, log.LstdFlags)
	ni.BaseInterceptor.Init(id, port, nm, logger)

	// Secrets files written by aptos_server.sh:
	//   ${BASE_DIR}/nodes/v${NODE_INDEX}/noise_secrets.jsonl
	baseDir := os.Getenv("BASE_DIR")
	if baseDir == "" {
		baseDir = "/tmp/aptos-dstest"
	}
	ni.keyReg = aptos.NewKeyRegistry(baseDir)
}

func (ni *AptosTCPInterceptor) Run() (err error) {
	err = ni.BaseInterceptor.Run()
	if err != nil {
		return err
	}

	ni.Log.Printf("Running AptosTCP interceptor on port %d\n", ni.Port)

	portSpecification := fmt.Sprintf(":%d", ni.Port)
	ni.Listener, err = net.Listen("tcp", portSpecification)

	if err != nil {
		ni.Log.Printf("Error listening on port %d: %s\n", ni.Port, err.Error())
		return err
	}

	ni.Log.Printf("Listening on port %d\n", ni.Port)

	go func() {
		for {
			conn, err := ni.Listener.Accept()
			if err != nil {
				ni.Log.Printf("Error accepting connection: %s\n", err.Error())
				return
			}
			go ni.handleConnection(conn)
		}
	}()

	return nil
}

func (ni *AptosTCPInterceptor) Shutdown() {
	if ni.Listener != nil {
		ni.Listener.Close()
	}
}

func (ni *AptosTCPInterceptor) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Get sender/receiver mapping based on interceptor port
	pair, ok := ni.NetworkManager.PortMap[ni.Port]
	if !ok {
		ni.Log.Printf("No port mapping found for port %d\n", ni.Port)
		return
	}

	sender := pair.Sender
	receiver := pair.Receiver

	// Calculate the actual listening port of the target node
	// The receiver node listens on BaseReplicaPort + receiver + 1
	targetPort := ni.NetworkManager.Config.NetworkConfig.BaseReplicaPort + receiver + 1
	targetAddr := fmt.Sprintf("127.0.0.1:%d", targetPort)

	sessionId, _ := rand.Int(rand.Reader, big.NewInt(100))
	ni.Log.Printf("[%d] Proxying connection: node%d -> node%d (target %s)\n", sessionId.Int64(), sender, receiver, targetAddr)

	// Connect to the target node (forward immediately; the TCP proxy bypasses the scheduler)
	targetConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		ni.Log.Printf("Error connecting to target %s: %s\n", targetAddr, err.Error())
		return
	}
	defer targetConn.Close()

	// Two-way proxy
	var wg sync.WaitGroup
	wg.Add(2)

	ni.Log.Printf(
		"Port %d mapping sender=%d receiver=%d clientRemote=%s",
		ni.Port,
		sender,
		receiver,
		clientConn.LocalAddr(),
	)

	//ni.skipHandshake(clientConn, targetConn)

	// client -> target
	go func() {
		defer wg.Done()
		defer ni.Log.Printf("[%d] Initiator->dstest session closed for node%d->node%d\n", sessionId.Int64(), sender, receiver)
		ni.session(clientConn, targetConn, sender, receiver, true, sessionId.Int64())
	}()

	// target -> client
	go func() {
		defer wg.Done()
		defer ni.Log.Printf("[%d] dstest<-Responder session closed for node%d->node%d\n", sessionId.Int64(), sender, receiver)
		ni.session(targetConn, clientConn, sender, receiver, false, sessionId.Int64())
	}()

	wg.Wait()
	ni.Log.Printf("[%d] Connection closed: node%d -> node%d\n", sessionId.Int64(), sender, receiver)
}

func (ni *AptosTCPInterceptor) skipHandshake(clientConn, serverConn net.Conn) {
	// 1) initiator -> responder handshake
	buf1 := make([]byte, 168)
	_, err := io.ReadFull(clientConn, buf1)
	if err != nil {
		ni.Log.Printf("Error reading handshake from initiator: %s\n", err.Error())
	}
	_, err = serverConn.Write(buf1)
	if err != nil {
		ni.Log.Printf("Error writing handshake to responder: %s\n", err.Error())
	}

	// 2) responder -> initiator handshake
	buf2 := make([]byte, 48)
	_, err = io.ReadFull(serverConn, buf2)
	if err != nil {
		ni.Log.Printf("Error reading handshake from responder: %s\n", err.Error())
	}
	_, err = clientConn.Write(buf2)
	if err != nil {
		ni.Log.Printf("Error writing handshake to initiator: %s\n", err.Error())
	}
}

func (ni *AptosTCPInterceptor) session(
	from, to net.Conn,
	sender, receiver int,
	forwardDir bool,
	sessionId int64) {

	framer := NewU16Framer()
	plainFramer := NewU32Framer()

	if tcp, ok := to.(*net.TCPConn); ok {
		defer tcp.CloseWrite()
	}

	keysBound := false
	var dk aptos.DialKeys
	var nonce uint64
	var key [32]byte

	ni.proxyAndTap(from, to, func(chunk []byte) {
		//ni.Log.Printf("Read: %v\n", chunk)
		if !keysBound {
			got, ok := ni.keyReg.GetKeysForDial(sender, receiver)
			if !ok {
				return
			}

			dk = got
			keysBound = true

			ni.keyReg.RefreshNode(sender)
			ni.keyReg.RefreshNode(receiver)

			if forwardDir {
				nonce = dk.R2S_Responder.ReadNonce0
				key = dk.R2S_Responder.ReadKey
			} else {
				nonce = dk.S2R_Initiator.ReadNonce0
				key = dk.S2R_Initiator.ReadKey
			}

			ni.Log.Printf(
				"[%d] BOUND proxy node%d<->node%d dir=%v iw=%s ir=%s remote=%s",
				sessionId, sender, receiver, forwardDir,
				hex.EncodeToString(dk.S2R_Initiator.WriteKey[:8]),
				hex.EncodeToString(dk.S2R_Initiator.ReadKey[:8]),
				hex.EncodeToString(dk.S2R_Initiator.RemoteStatic[:8]),
			)
		}

		// Post-handshake
		// Parse frames and try to decrypt
		frames := framer.Parse(chunk)

		for _, fr := range frames {

			ni.Log.Printf("dir=%v key_head=%s nonce0=%d", forwardDir, hex.EncodeToString(key[:])[:16], nonce)

			pt, err := aptos.DecryptNoiseFrame(key, nonce, fr)

			curNonce := nonce
			nonce++

			if err != nil {
				ni.Log.Printf("[%d] Decrypt failed node%d->node%d dir=%v nonce=%d err=%v head=%s",
					sessionId, sender, receiver, forwardDir, curNonce, err, headHex(fr, 16))
				continue
			}

			ni.Log.Printf("[%d] Decrypted node%d->node%d dir=%v nonce=%d pt_len=%d pt_head=%s",
				sessionId, sender, receiver, forwardDir, curNonce, len(pt), headHex(pt, 32))

			// After decryption, the plaintext is framed as [u32_be len][len bytes of payload],
			// where the payload is a BCS-serialized MultiplexMessage
			msgs := plainFramer.Parse(pt)
			ni.Log.Printf("msg_len=%d", len(msgs))
			for _, m := range msgs {
				// m is one full BCS-serialized MultiplexMessage
				msg, protocolId, err := ni.decodeNetworkMessage(m, sender, receiver, forwardDir, sessionId)
				if err != nil {
					ni.Log.Printf("Failed to deserialize message: %v\n", err)
					continue
				}
				if protocolId == nil || !protocolId.IsConsensus() {
					continue
				}
				ni.Log.Printf("Decoded message: node%d->node%d dir=%v sessionId=%d env={Variant=%s ProtocolID=%s PayloadLen=%d PayloadHead=%s}\n",
					sender, receiver, forwardDir, sessionId, msg.Variant, msg.ProtocolID, len(msg.Payload), headHex(msg.Payload, 32),
				)

				err = ni.decodeConsensusMessage(msg, protocolId)
				if err != nil {
					ni.Log.Printf("Failed to decode consensus message: %v\n", err)
				}
			}
		}
	})
}

// proxyAndTap forwards raw bytes immediately (so handshake is never blocked)
// and optionally feeds the same bytes to a parser via tap().
// We want to parse the bytes that contain consensus messages, hence post-handshake.
func (ni *AptosTCPInterceptor) proxyAndTap(inConn net.Conn, outConn net.Conn, tap func([]byte)) {
	buf := make([]byte, 32*1024)

	for {
		n, err := inConn.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			// Always forward immediately
			if err2 := writeFull(outConn, chunk); err2 != nil {
				return
			}

			// Side-effect tap (never blocks forwarding)
			if tap != nil {
				ni.Log.Println("Read chunk of size", len(chunk))
				tap(chunk)
			}
		}

		if err != nil {
			// io.EOF or connection error
			return
		}
	}
}

func (ni *AptosTCPInterceptor) debugKeysForPair(sender, receiver int) {
	ni.keyReg.Mu.RLock()
	defer ni.keyReg.Mu.RUnlock()

	ni.Log.Printf("=== KeyRegistry relevant dump for link node%d<->node%d (total=%d) ===",
		sender, receiver, len(ni.keyReg.Sessions))

	for k, v := range ni.keyReg.Sessions {
		if k.Node != sender && k.Node != receiver {
			continue
		}
		ni.Log.Printf("node=%d event=%s remote=%s write_nonce0=%d read_nonce0=%d",
			k.Node, k.Event, k.RsHex[:16], v.WriteNonce0, v.ReadNonce0)
	}
}

// Helper functions
// -----------------
func writeFull(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

func fileHasData(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Size() > 0
}

func headHex(b []byte, n int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > n {
		b = b[:n]
	}
	return hex.EncodeToString(b)
}

//
// U16 framer: extracts frames from an encrypted Noise stream: [u16_be len][len bytes of encrypted msg]
//

type U16Framer struct {
	buf      []byte
	expected int // 0 means "need len"; >0 means we already read the len, and we are waiting for that many bytes
}

func NewU16Framer() *U16Framer {
	return &U16Framer{
		buf:      make([]byte, 0, 64*1024),
		expected: 0,
	}
}

func (f *U16Framer) Reset() {
	f.buf = f.buf[:0]
	f.expected = 0
}

// Parse returns 0 or more complete frames (without the 2-byte len prefix).
// This is for post-handshake NoiseStream traffic only.
func (f *U16Framer) Parse(chunk []byte) (frames [][]byte) {
	f.buf = append(f.buf, chunk...)

	for {
		if f.expected == 0 {
			if len(f.buf) < 2 {
				return frames
			}
			f.expected = int(binary.BigEndian.Uint16(f.buf[:2]))
			f.buf = f.buf[2:]

			// Aptos treats 0-length as EOF / invalid
			if f.expected <= 0 || f.expected > 65535 {
				// Reset parser state (do not stop forwarding)
				f.expected = 0
				f.buf = f.buf[:0]
				return frames
			}
		}

		//Not a full frame, wait for more bytes
		if len(f.buf) < f.expected {
			return frames
		}

		//We have a full message
		//Extract the frame
		frame := make([]byte, f.expected)
		copy(frame, f.buf[:f.expected])
		f.buf = f.buf[f.expected:]
		f.expected = 0

		frames = append(frames, frame)
	}
}

// U32 framer: extracts frames from a decrypted plaintext stream [u32_be len][len bytes]
type U32Framer struct {
	buf      []byte
	expected int // 0 means "need 4-byte len"
}

func NewU32Framer() *U32Framer {
	return &U32Framer{
		buf:      make([]byte, 2, 128*1024),
		expected: 0,
	}
}

func (f *U32Framer) Reset() {
	f.buf = f.buf[:0]
	f.expected = 0
}

// Parse returns 0 or more complete frames for the decrypted plaintext,
// where each frame is the payload without the 4-byte len prefix.
// [u32_be len][len bytes]
func (f *U32Framer) Parse(chunk []byte) (frames [][]byte) {
	f.buf = append(f.buf, chunk...)

	for {
		if f.expected == 0 {
			if len(f.buf) < 4 {
				return frames
			}
			f.expected = int(binary.BigEndian.Uint32(f.buf[:4]))
			f.buf = f.buf[4:]

			if f.expected <= 0 || f.expected > 16*1024*1024 {
				// Reset state
				f.expected = 0
				f.buf = f.buf[:0]
				return frames
			}
		}

		if len(f.buf) < f.expected {
			return frames
		}

		frame := make([]byte, f.expected)
		copy(frame, f.buf[:f.expected])
		f.buf = f.buf[f.expected:]
		f.expected = 0

		frames = append(frames, frame)
	}
}

// AptosNetworkEnvelope represents the decoded contents of a NoiseStream frame after decryption
type AptosNetworkEnvelope struct {
	Variant    string // "DirectSendMsg", "RpcRequest", "RpcResponse"
	ProtocolID string
	Payload    []byte
}

type MultiplexMessage struct {
	Message *AptosNetworkMessage
	Stream  any `bcs:"-"`
}

func (e MultiplexMessage) IsBcsEnum()    {}
func (e AptosNetworkMessage) IsBcsEnum() {}
func (e ProtocolId) IsBcsEnum()          {}

type AptosNetworkMessage struct {
	Error         any
	RpcRequest    *RpcRequest
	RpcResponse   *RpcResponse
	DirectSendMsg *DirectSendMsg
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

// Based on ProtocolID, we know its serialization: BCS, JSON, or compressed.
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

func (ni *AptosTCPInterceptor) decodeNetworkMessage(
	m []byte,
	sender int,
	receiver int,
	forwardDir bool,
	sessionId int64,
) (*AptosNetworkEnvelope, *ProtocolId, error) {

	v := &MultiplexMessage{}
	if _, err := bcs.Unmarshal(m, v); err != nil {
		return nil, nil, fmt.Errorf("Failed to unmarshal MultiplexMessage: %w", err)
	}
	ni.Log.Printf("Decoded MultiplexMessage node%d->node%d dir=%v sessionId=%d msg=%+v", sender, receiver, forwardDir, sessionId, v)

	msg := v.Message

	if msg == nil {
		return nil, nil, fmt.Errorf("MultiplexMessage does not contain a Message")
	}

	ni.Log.Printf(
		"Decoded AptosNetworkMessage node%d->node%d dir=%v sessionId=%d msg=%+v",
		sender, receiver, forwardDir, sessionId, msg,
	)

	env := &AptosNetworkEnvelope{}
	protocolId := &ProtocolId{}

	switch {
	case msg.DirectSendMsg != nil:
		env.Variant = "DirectSendMsg"
		env.ProtocolID = msg.DirectSendMsg.ProtocolID.String()
		protocolId = msg.DirectSendMsg.ProtocolID
		env.Payload = msg.DirectSendMsg.RawMsg

	case msg.RpcRequest != nil:
		env.Variant = "RpcRequest"
		env.ProtocolID = msg.RpcRequest.ProtocolID.String()
		protocolId = msg.RpcRequest.ProtocolID
		env.Payload = msg.RpcRequest.RawRequest

	case msg.RpcResponse != nil:
		env.Variant = "RpcResponse"
		env.ProtocolID = ""
		protocolId = nil
		env.Payload = msg.RpcResponse.RawResponse

	case msg.Error != nil:
		env.Variant = "Error"
		env.ProtocolID = ""
		protocolId = nil
		env.Payload = nil

	default:
		return nil, nil, fmt.Errorf("Decoded message does not have the right form: neither DirectSendMsg, RpcRequest, RpcResponse nor Error is set")
	}

	ni.Log.Printf(
		"Decoded AptosNetworkEnvelope node%d->node%d dir=%v sessionId=%d env={Variant=%s ProtocolID=%s PayloadLen=%d PayloadHead=%s}",
		sender, receiver, forwardDir, sessionId, env.Variant, env.ProtocolID, len(env.Payload), headHex(env.Payload, 32),
	)
	return env, protocolId, nil
}

// Decodes the payload of the envelope based on the variant and protocol ID
// For consensus messages, we expect:
//
//	Variant = "DirectSendMsg|RpcRequest|RpcResponse"
//	ProtocolID = "ConsensusRpcBcs|ConsensusDirectSendBcs|ConsensusRpcJson|ConsensusDirectSendJson|ConsensusRpcCompressed|ConsensusDirectSendCompressed"
//
// The payload is a protobuf message of type ConsensusMsg
func (ni *AptosTCPInterceptor) decodeConsensusMessage(env *AptosNetworkEnvelope, protocolID *ProtocolId) error {
	var cmsg ConsensusMsg
	if err := protocolID.DecodeInto(env.Payload, &cmsg); err != nil {
		return err
	}

	ni.Log.Printf("Consensus payload decoded: %+v", cmsg)
	return nil
}
