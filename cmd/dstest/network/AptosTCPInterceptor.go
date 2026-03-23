package network

import (
	"crypto/rand"
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

	framer := aptos.NewU16Framer()
	plainFramer := aptos.NewU32Framer()

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

			ni.deserializeMessage(pt, sender, receiver, forwardDir, sessionId, plainFramer)
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

func headHex(b []byte, n int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > n {
		b = b[:n]
	}
	return hex.EncodeToString(b)
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

// Aptos deserialization of decrypted payloads
// -----------------------------

func (ni *AptosTCPInterceptor) deserializeMessage(
	pt []byte,
	sender int,
	receiver int,
	forwardDir bool,
	sessionId int64,
	plainFramer *aptos.U32Framer,
) {
	// After decryption, a plaintext is framed as [u32_be len][len bytes of payload],
	// where the payload is a BCS-serialized MultiplexMessage
	msgs := plainFramer.Parse(pt)
	ni.Log.Printf("msg_len=%d", len(msgs))

	for _, m := range msgs {
		// m is one full BCS-serialized Aptos MultiplexMessage
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

func (ni *AptosTCPInterceptor) decodeNetworkMessage(
	m []byte,
	sender int,
	receiver int,
	forwardDir bool,
	sessionId int64,
) (*aptos.AptosNetworkEnvelope, *aptos.ProtocolId, error) {

	v := &aptos.MultiplexMessage{}
	if err := bcs.UnmarshalAll(m, v); err != nil {
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

	env := &aptos.AptosNetworkEnvelope{}
	protocolId := &aptos.ProtocolId{}

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
func (ni *AptosTCPInterceptor) decodeConsensusMessage(env *aptos.AptosNetworkEnvelope, protocolID *aptos.ProtocolId) error {
	decoded, consensusTag, consensusTagLen, err := protocolID.DecodeConsensusPayload(env.Payload)

	if err != nil {
		return err
	}

	// The body is the remaining bytes after the enum tag
	consensusBody := decoded[consensusTagLen:]

	switch consensusTag {
	case 3: //ProposalMsg
		var proposalMsg aptos.ProposalMsg
		if err := bcs.UnmarshalAll(consensusBody, &proposalMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal ProposalMsg: %w", err)
		}
		ni.Log.Printf("Decoded ProposalMsg: %v", proposalMsg)
		fmt.Printf("%s", proposalMsg.String())

	case 21: // OptProposalMsg
		var optProposalMsg aptos.OptProposalMsg
		if err := bcs.UnmarshalAll(consensusBody, &optProposalMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal OptProposalMsg: %w", err)
		}
		ni.Log.Printf("Decoded OptProposalMsg: %v", optProposalMsg)
		fmt.Printf("%s", optProposalMsg.String())

	case 6: // VoteMsg
		var voteMsg aptos.VoteMsg
		if err := bcs.UnmarshalAll(consensusBody, &voteMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal VoteMsg: %w", err)
		}
		ni.Log.Printf("Decoded VoteMsg: %v", voteMsg)
		fmt.Printf("%s", voteMsg.String())

	case 7: // CommitVoteMsg
		var commitVoteMsg aptos.CommitVote
		if err := bcs.UnmarshalAll(consensusBody, &commitVoteMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal CommitVoteMsg: %w", err)
		}
		ni.Log.Printf("Decoded CommitVoteMsg: %v", commitVoteMsg)
		fmt.Printf("%s", commitVoteMsg.String())

	case 15: // CommitMessage
		var commitMsg aptos.CommitMessage
		if err := bcs.UnmarshalAll(consensusBody, &commitMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal CommitMsg: %w", err)
		}
		ni.Log.Printf("Decoded CommitMsg: %v", commitMsg)
		fmt.Printf("%s", commitMsg.String())

	case 19: // RoundTimeoutMsg
		var roundTimeoutMsg aptos.RoundTimeoutMsg
		if err := bcs.UnmarshalAll(consensusBody, &roundTimeoutMsg); err != nil {
			return fmt.Errorf("Failed to unmarshal RoundTimeoutMsg: %w", err)
		}
		ni.Log.Printf("Decoded RoundTimeoutMsg: %v", roundTimeoutMsg)
		fmt.Printf("%s", roundTimeoutMsg.String())
	}

	ni.Log.Printf("Consensus payload decoded: %s", aptos.ConsensusMsgVariantName(consensusTag))
	return nil
}

func (ni *AptosTCPInterceptor) debug(consensusBody []byte) error {
	return nil
}
