package network

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"slices"
	"sync"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"github.com/fardream/go-bcs/bcs"
)

type AptosTCPInterceptor struct {
	BaseInterceptor
	Listener net.Listener
}

//------------------------------

var notImplementedErr = fmt.Errorf("Handler not implemented")
var notConsensusMsgErr = fmt.Errorf("Not a consensus message")

type NoiseLayer struct {
	noiseSession *aptos.NoiseSession
	Log          *log.Logger
	Framer       *aptos.U16Framer
	InConn       net.Conn
	OutConn      net.Conn
}

func NewNoiseLayer(
	in, out net.Conn,
	sender, receiver int,
	forwardDir bool,
	logger *log.Logger,
) (NoiseLayer, error) {
	// Secrets files written by aptos_server.sh:
	//   ${BASE_DIR}/nodes/v${NODE_INDEX}/noise_secrets.jsonl
	baseDir := os.Getenv("BASE_DIR")
	if baseDir == "" {
		baseDir = "/tmp/aptos-dstest"
	}
	keyReg := aptos.NewKeyRegistry(baseDir)

	var nonce uint64
	var key [32]byte
	var noiseSession *aptos.NoiseSession

	dk, ok := keyReg.GetKeysForDial(sender, receiver)
	if !ok {
		// Keys not ready yet.
		return NoiseLayer{}, fmt.Errorf("Keys not ready for node%d->node%d", sender, receiver)
	}

	if forwardDir {
		nonce = dk.R2S_Responder.ReadNonce0
		key = dk.R2S_Responder.ReadKey
	} else {
		nonce = dk.S2R_Initiator.ReadNonce0
		key = dk.S2R_Initiator.ReadKey
	}

	noiseSession = aptos.NewNoiseSession(dk, key, nonce, forwardDir, sender, receiver, 0)
	return NoiseLayer{
		noiseSession: noiseSession,
		Log:          logger,
		Framer:       aptos.NewU16Framer(),
		InConn:       in,
		OutConn:      out,
	}, nil
}

func (n *NoiseLayer) Read(r [][]byte) (int, error) {
	// Post-handshake
	// Parse frames and try to decrypt
	chunk := make([]byte, 32*1024)
	cnt, err := n.InConn.Read(chunk)
	if err != nil {
		return 0, err
	}
	chunk = chunk[:cnt]
	frames := n.Framer.Parse(chunk)
	for i, fr := range frames {
		pt, _, err := n.noiseSession.DecryptNoiseFrame(fr)

		if err != nil {
			n.Log.Printf("[%d] Decrypt failed node%d->node%d dir=%v",
				n.noiseSession.SessionId, n.noiseSession.Sender, n.noiseSession.Receiver, n.noiseSession.ForwardDir)
			return 0, err
		}

		n.Log.Printf("[%d] Decrypted node%d->node%d dir=%v",
			n.noiseSession.SessionId, n.noiseSession.Sender, n.noiseSession.Receiver, n.noiseSession.ForwardDir)

		r[i] = pt
	}

	return len(frames), nil
}

func (n *NoiseLayer) Write(p []byte) error {
	u16 := uint16(len(p))
	ciphertext, err := n.noiseSession.EncryptNoiseFrame(p)
	if err != nil {
		return err
	}

	buf := make([]byte, 2+len(ciphertext))
	binary.BigEndian.PutUint16(buf[:2], u16)
	copy(buf[2:], ciphertext)
	return writeFull(n.OutConn, buf)
}

type U32FrameLayer struct {
	NoiseLayer
	PlainFramer *aptos.U32Framer
}

func (f *U32FrameLayer) Read(r [][]byte) (int, error) {
	buf := make([][]byte, 1024)
	n, err := f.NoiseLayer.Read(buf)
	if err != nil {
		return 0, err
	}
	// After decryption, a plaintext is framed as [u32_be len][len bytes of payload],
	// where the payload is a BCS-serialized MultiplexMessage
	// buf now contains n bytes of decrypted plaintext, which may be multiple framed messages
	// Parse frames and place results in r
	pt := slices.Concat(buf[:n]...)
	msgs := f.PlainFramer.Parse(pt)
	f.Log.Printf("msg_len=%d", len(msgs))

	for i, m := range msgs {
		r[i] = m
	}

	return len(msgs), nil
}

func (f *U32FrameLayer) Write(p []byte) error {
	n := len(p)
	buf := make([]byte, 4+n)
	binary.BigEndian.PutUint32(buf[:4], uint32(n))
	copy(buf[4:], p)
	err := f.NoiseLayer.Write(buf)
	return err
}

type NetworkMsgLayer struct {
	U32FrameLayer
}

func (nw *NetworkMsgLayer) Read(r []aptos.AptosNetworkEnvelope) (int, error) {
	buf := make([][]byte, 1024)
	n, err := nw.U32FrameLayer.Read(buf)
	if err != nil {
		return 0, err
	}
	// buf now contains n bytes of decrypted plaintext, which may be multiple framed messages
	// Parse frames and decode each one as an AptosNetworkEnvelope, placing results in r
	successful := 0
	for _, framePt := range buf[:n] {
		// framePt is one full BCS-serialized Aptos MultiplexMessage
		tmp, err := nw.decodeNetworkMessage(framePt)
		if err != nil {
			nw.Log.Printf("Failed to decode network message: %v\n", err)
			nw.U32FrameLayer.Write(framePt)
		} else {
			r[successful] = tmp
			successful++
		}

	}
	return successful, nil
}

// TODO
func (nw *NetworkMsgLayer) Write(p aptos.AptosNetworkEnvelope) error {
	return nw.U32FrameLayer.Write(p.Payload)
}

func (l *NetworkMsgLayer) decodeNetworkMessage(
	m []byte,
) (aptos.AptosNetworkEnvelope, error) {

	v := &aptos.MultiplexMessage{}
	if err := bcs.UnmarshalAll(m, v); err != nil {
		return aptos.AptosNetworkEnvelope{}, fmt.Errorf("Failed to unmarshal MultiplexMessage: %w", err)
	}
	l.Log.Printf("Decoded MultiplexMessage node%d->node%d dir=%v sessionId=%d msg=%+v",
		l.noiseSession.Sender,
		l.noiseSession.Receiver,
		l.noiseSession.ForwardDir,
		l.noiseSession.SessionId,
		v,
	)

	msg := v.Message
	if msg == nil {
		return aptos.AptosNetworkEnvelope{}, fmt.Errorf("MultiplexMessage does not contain a Message")
	}

	l.Log.Printf(
		"Decoded AptosNetworkMessage node%d->node%d dir=%v sessionId=%d msg=%+v",
		l.noiseSession.Sender, l.noiseSession.Receiver, l.noiseSession.ForwardDir, l.noiseSession.SessionId, msg,
	)

	env := &aptos.AptosNetworkEnvelope{}

	switch {
	case msg.DirectSendMsg != nil:
		env.Variant = "DirectSendMsg"
		env.ProtocolId = msg.DirectSendMsg.ProtocolID
		env.Payload = msg.DirectSendMsg.RawMsg

	case msg.RpcRequest != nil:
		env.Variant = "RpcRequest"
		env.ProtocolId = msg.RpcRequest.ProtocolID
		env.Payload = msg.RpcRequest.RawRequest

	case msg.RpcResponse != nil:
		env.Variant = "RpcResponse"
		env.ProtocolId = nil
		env.Payload = msg.RpcResponse.RawResponse

	case msg.Error != nil:
		env.Variant = "Error"
		env.ProtocolId = nil
		env.Payload = nil

	default:
		return aptos.AptosNetworkEnvelope{}, fmt.Errorf("Decoded message does not have the right form: neither DirectSendMsg, RpcRequest, RpcResponse nor Error is set")
	}

	l.Log.Printf(
		"Decoded AptosNetworkEnvelope node%d->node%d dir=%v sessionId=%d env={Variant=%s ProtocolID=%s PayloadLen=%d PayloadHead=%s}",
		l.noiseSession.Sender, l.noiseSession.Receiver, l.noiseSession.ForwardDir, l.noiseSession.SessionId, env.Variant, env.ProtocolId, len(env.Payload), headHex(env.Payload, 32),
	)
	return *env, nil
}

type ConsensusMsgLayer struct {
	NetworkMsgLayer
}

func (c *ConsensusMsgLayer) Read(r []aptos.IConsensusMessage) (int, error) {
	buf := make([]aptos.AptosNetworkEnvelope, 1024)
	n, err := c.NetworkMsgLayer.Read(buf)
	if err != nil {
		return 0, err
	}

	cnt := 0
	for _, env := range buf[:n] {
		tmp, err := c.decodeConsensusMessage(&env)
		if err != nil {
			c.NetworkMsgLayer.Write(env)
		} else {
			r[cnt] = tmp
			cnt++
		}
	}
	return cnt, nil
}

// TODO
func (c *ConsensusMsgLayer) Write(p aptos.IConsensusMessage) error {
	return nil
}

// Decodes the payload of the envelope based on the variant and protocol ID
// For consensus messages, we expect:
//
//	Variant = "DirectSendMsg|RpcRequest|RpcResponse"
//	ProtocolID = "ConsensusRpcBcs|ConsensusDirectSendBcs|ConsensusRpcJson|ConsensusDirectSendJson|ConsensusRpcCompressed|ConsensusDirectSendCompressed"
//
// The payload is a protobuf message of type ConsensusMsg
func (c *ConsensusMsgLayer) decodeConsensusMessage(env *aptos.AptosNetworkEnvelope) (aptos.IConsensusMessage, error) {
	if env.ProtocolId == nil || !env.ProtocolId.IsConsensus() {
		return nil, notConsensusMsgErr
	}

	decoded, consensusTag, consensusTagLen, err := env.ProtocolId.DecodeConsensusPayload(env.Payload)
	if err != nil {
		return nil, err
	}

	// The body is the remaining bytes after the enum tag
	consensusBody := decoded[consensusTagLen:]
	var msgType aptos.IConsensusMessage

	switch consensusTag {
	case 3: //ProposalMsg
		msgType = &aptos.ProposalMsg{}

	case 21: // OptProposalMsg
		msgType = &aptos.OptProposalMsg{}

	case 6: // VoteMsg
		msgType = &aptos.VoteMsg{}

	case 7: // CommitVoteMsg
		msgType = &aptos.CommitVote{}

	case 15: // CommitMessage
		msgType = &aptos.CommitMessage{}

	case 19: // RoundTimeoutMsg
		msgType = &aptos.RoundTimeoutMsg{}
	}

	if err := bcs.UnmarshalAll(consensusBody, msgType); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal: %v %w", msgType, err)
	}

	c.Log.Printf("Decoded: %v", msgType)
	c.Log.Printf("Consensus payload decoded: %s", aptos.ConsensusMsgVariantName(consensusTag))
	return msgType, nil
}

// Check if BaseInterceptor implements Interceptor interface
var _ Interceptor = (*AptosTCPInterceptor)(nil)

func (ni *AptosTCPInterceptor) Init(id int, port int, nm *Manager) {
	logPrefix := fmt.Sprintf("[AptosTCP Interceptor %d] ", id)
	logger := log.New(log.Writer(), logPrefix, log.LstdFlags)
	ni.BaseInterceptor.Init(id, port, nm, logger)
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

	ni.skipHandshake(clientConn, targetConn)

	// client -> target
	go func() {
		defer wg.Done()
		defer ni.Log.Printf("[%d] Initiator->dstest session closed for node%d->node%d\n", sessionId.Int64(), sender, receiver)
		ni.session(clientConn, targetConn, sender, receiver, true)
	}()

	// target -> client
	go func() {
		defer wg.Done()
		defer ni.Log.Printf("[%d] dstest<-Responder session closed for node%d->node%d\n", sessionId.Int64(), sender, receiver)
		ni.session(targetConn, clientConn, sender, receiver, false)
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
) {

	if tcp, ok := to.(*net.TCPConn); ok {
		defer tcp.CloseWrite()
	}

	nLayer, err := NewNoiseLayer(from, to, sender, receiver, forwardDir, ni.Log)
	if err != nil {
		return
	}

	socket := ConsensusMsgLayer{
		NetworkMsgLayer{
			U32FrameLayer{
				NoiseLayer:  nLayer,
				PlainFramer: aptos.NewU32Framer(),
			},
		},
	}

	for {
		buf := make([]aptos.IConsensusMessage, 16)
		n, err := socket.Read(buf)
		if err == io.EOF || n == 0 {
			return
		}
		if err != nil {
			ni.Log.Printf("Error reading aptos consensus: %v", err)
			return
		}
		buf = buf[:n]
		for _, msg := range buf {
			// queue the request in the network manager
			// we only queue successfully decoded consensus messages
			// in order to apply scheduling decisions to them
			// all other messages are forwarded immediately without queuing
			// awaitSendRequest := make(chan struct{})
			// networkMsg := &Message{
			// 	Sender:    sender,
			// 	Receiver:  receiver,
			// 	Payload:   msg,
			// 	Type:      Aptos,
			// 	Name:      "Aptos Consensus Message",
			// 	MessageId: ni.NetworkManager.GenerateUniqueId(),
			// 	Send:      awaitSendRequest,
			// }

			// ni.NetworkManager.Router.QueueMessage(networkMsg)
			// <-awaitSendRequest

			socket.Write(msg)
		}
	}
}

// Helper functions
// -----------------
func writeFull(conn io.Writer, buf []byte) error {
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
