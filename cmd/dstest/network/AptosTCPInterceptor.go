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
	"slices"
	"sync"
	"time"

	aptos "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
	"github.com/fardream/go-bcs/bcs"
)

type AptosTCPInterceptor struct {
	BaseInterceptor
	Listener net.Listener
	keyReg   *aptos.KeyRegistry
}

var notImplementedErr = fmt.Errorf("Handler not implemented")
var notConsensusMsgErr = fmt.Errorf("Not a consensus message")
var decryptFailedErr = fmt.Errorf("Decrypt failed")

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
	keyReg *aptos.KeyRegistry,
) (NoiseLayer, error) {
	var nonce uint64
	var key [32]byte
	var noiseSession *aptos.NoiseSession

	var dk aptos.DialKeys
	for {
		var ok bool
		dk, ok = keyReg.GetKeysForDial(sender, receiver)
		if !ok {
			logger.Printf("Keys not ready for node%d->node%d\n", sender, receiver)
		} else {
			break
		}
		time.Sleep(10 * time.Millisecond)
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
	ciphertext, err := n.noiseSession.EncryptNoiseFrame(p)
	if err != nil {
		return err
	}
	u16 := uint16(len(ciphertext))
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
	for i, m := range msgs {
		r[i] = m
	}

	return len(msgs), nil
}

func (f *U32FrameLayer) Write(p []byte) error {
	var buf []byte
	if f.noiseSession.NoSentMessages() {
		buf = aptos.Frame(aptos.U16, p) // weird thing in the protocol, the first message is framed with u16 at this layer
	} else {
		buf = aptos.Frame(aptos.U32, p)
	}
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
		tmp, err := nw.decodeNetworkMessage(bytes.Clone(framePt))
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

func (nw *NetworkMsgLayer) Write(p aptos.AptosNetworkEnvelope) error {
	payload := p.Payload
	if p.ProtocolId != nil && p.ProtocolId.IsConsensus() {
		encoded, err := p.EncodeConsensusPayload()
		if err != nil {
			return fmt.Errorf("Failed to encode (compress) consensus payload: %w", err)
		}
		payload = encoded
	}

	nm, err := nw.encodeNetworkMessage(payload, p)
	if err != nil {
		return fmt.Errorf("Failed to encode network message: %w", err)
	}

	err = nw.U32FrameLayer.Write(nm)
	return err
}

func (l *NetworkMsgLayer) encodeNetworkMessage(encoded []byte, env aptos.AptosNetworkEnvelope) ([]byte, error) {
	var netMsg aptos.NetworkMessage

	switch {
	case env.Variant.DirectSendMsg != nil:
		netMsg.DirectSendMsg = &aptos.DirectSendMsg{
			ProtocolID: env.ProtocolId,
			Priority:   env.Variant.DirectSendMsg.Priority,
			RawMsg:     encoded,
		}
	case env.Variant.RpcRequest != nil:
		netMsg.RpcRequest = &aptos.RpcRequest{
			ProtocolID: env.ProtocolId,
			RequestID:  env.Variant.RpcRequest.RequestID,
			Priority:   env.Variant.RpcRequest.Priority,
			RawRequest: encoded,
		}
	case env.Variant.RpcResponse != nil:
		netMsg.RpcResponse = &aptos.RpcResponse{
			RequestID:   env.Variant.RpcResponse.RequestID,
			Priority:    env.Variant.RpcResponse.Priority,
			RawResponse: encoded,
		}
	}

	mux := aptos.MultiplexMessage{Message: &netMsg}
	encodedMux, err := bcs.Marshal(mux)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal MultiplexMessage: %w", err)
	}

	return encodedMux, nil
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
		env.Variant = *msg
		env.ProtocolId = msg.DirectSendMsg.ProtocolID
		env.Payload = msg.DirectSendMsg.RawMsg

	case msg.RpcRequest != nil:
		env.Variant = *msg
		env.ProtocolId = msg.RpcRequest.ProtocolID
		env.Payload = msg.RpcRequest.RawRequest

	case msg.RpcResponse != nil:
		env.Variant = *msg
		env.ProtocolId = nil
		env.Payload = msg.RpcResponse.RawResponse

	case msg.Error != nil:
		env.Variant = *msg
		env.ProtocolId = nil
		env.Payload = nil

	default:
		return aptos.AptosNetworkEnvelope{}, fmt.Errorf("Decoded message does not have the right form: neither DirectSendMsg, RpcRequest, RpcResponse nor Error is set")
	}

	l.Log.Printf(
		"Decoded AptosNetworkEnvelope node%d->node%d dir=%v sessionId=%d env={Variant=%+v ProtocolID=%s PayloadLen=%d PayloadHead=%s}",
		l.noiseSession.Sender, l.noiseSession.Receiver, l.noiseSession.ForwardDir, l.noiseSession.SessionId, env.Variant, env.ProtocolId, len(env.Payload), headHex(env.Payload, 32),
	)
	return *env, nil
}

type ConsensusMsgLayer struct {
	NetworkMsgLayer
}

func (c *ConsensusMsgLayer) Read(r []aptos.DecodedConsensusMsg) (int, error) {
	buf := make([]aptos.AptosNetworkEnvelope, 1024)
	n, err := c.NetworkMsgLayer.Read(buf)
	if err != nil {
		return 0, err
	}

	cnt := 0
	for _, env := range buf[:n] {
		tmp, err := c.decodeConsensusMessage(&env)
		if err != nil {
			err2 := c.NetworkMsgLayer.Write(env)
			c.Log.Printf("Decode error: %v, Forwarding error: %v\n", err, err2)
		} else {
			r[cnt] = tmp
			cnt++
		}
	}
	return cnt, nil
}

func (c *ConsensusMsgLayer) Write(msg aptos.DecodedConsensusMsg) error {
	consensusBody, err := bcs.Marshal(msg.Msg)
	if err != nil {
		return fmt.Errorf("Failed to marshal consensus message: %w", err)
	}

	consensusTag := aptos.EncodeConsensusTag(msg.ConsensusTag)
	consensusPayload := append([]byte{byte(consensusTag)}, consensusBody...)

	env := aptos.AptosNetworkEnvelope{
		Variant:    msg.Envelope.Variant,
		ProtocolId: msg.Envelope.ProtocolId,
		Payload:    consensusPayload, //serialized payload
	}

	err = c.NetworkMsgLayer.Write(env)
	return err
}

// Decodes the payload of the envelope based on the variant and protocol ID
// For consensus messages, we expect:
//
//	Variant = "DirectSendMsg|RpcRequest|RpcResponse"
//	ProtocolID = "ConsensusRpcBcs|ConsensusDirectSendBcs|ConsensusRpcJson|ConsensusDirectSendJson|ConsensusRpcCompressed|ConsensusDirectSendCompressed"
//
// The payload is a protobuf message of type ConsensusMsg
func (c *ConsensusMsgLayer) decodeConsensusMessage(env *aptos.AptosNetworkEnvelope) (aptos.DecodedConsensusMsg, error) {
	if env.ProtocolId == nil || !env.ProtocolId.IsConsensus() {
		return aptos.DecodedConsensusMsg{}, notConsensusMsgErr
	}

	decoded, consensusTag, consensusTagLen, err := env.ProtocolId.DecodeConsensusPayload(env.Payload)
	if err != nil {
		return aptos.DecodedConsensusMsg{}, err
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

	case 8: // CommitDecisionMsg
		msgType = &aptos.CommitDecision{}

	case 15: // CommitMessage
		msgType = &aptos.CommitMessage{}

	case 19: // RoundTimeoutMsg
		msgType = &aptos.RoundTimeoutMsg{}
	}

	if err := bcs.UnmarshalAll(consensusBody, msgType); err != nil {
		return aptos.DecodedConsensusMsg{}, fmt.Errorf("Failed to unmarshal: %v %w", msgType, err)
	}

	c.Log.Printf("Decoded: %v", msgType)
	c.Log.Printf("Consensus payload decoded: %s", aptos.ConsensusMsgVariantName(consensusTag))

	decodedMsg := aptos.DecodedConsensusMsg{
		Envelope:     *env,
		ConsensusTag: consensusTag,
		Msg:          msgType,
	}

	return decodedMsg, nil
}

// Check if BaseInterceptor implements Interceptor interface
var _ Interceptor = (*AptosTCPInterceptor)(nil)

func (ni *AptosTCPInterceptor) Init(id int, port int, nm *Manager) {
	logPrefix := fmt.Sprintf("[AptosTCP Interceptor %d] ", id)
	logger := log.New(log.Writer(), logPrefix, log.LstdFlags)

	// Secrets files written by aptos_server.sh:
	//   ${BASE_DIR}/nodes/v${NODE_INDEX}/noise_secrets.jsonl
	baseDir := os.Getenv("BASE_DIR")
	if baseDir == "" {
		baseDir = "/tmp/aptos-dstest"
	}
	ni.keyReg = aptos.NewKeyRegistry(baseDir)

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

	// Connect to the target node
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

	if err := ni.skipHandshake(clientConn, targetConn); err != nil {
		ni.Log.Printf("[%d] Handshake failed for node%d->node%d: %v\n. Conn is closed, node will redial when ready", sessionId.Int64(), sender, receiver, err)
		return
	}

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

func (ni *AptosTCPInterceptor) skipHandshake(clientConn, serverConn net.Conn) error {
	// 1) initiator -> responder handshake
	buf1 := make([]byte, 168)
	_, err := io.ReadFull(clientConn, buf1)
	if err != nil {
		return fmt.Errorf("reading handshake from initiator: %w", err)
	}
	_, err = serverConn.Write(buf1)
	if err != nil {
		return fmt.Errorf("writing handshake to responder: %w", err)
	}

	// 2) responder -> initiator handshake
	buf2 := make([]byte, 48)
	_, err = io.ReadFull(serverConn, buf2)
	if err != nil {
		return fmt.Errorf("reading handshake from responder: %w", err)
	}
	_, err = clientConn.Write(buf2)
	if err != nil {
		return fmt.Errorf("writing handshake to initiator: %w", err)
	}
	return nil
}

func (ni *AptosTCPInterceptor) session(
	from, to net.Conn,
	sender, receiver int,
	forwardDir bool,
) {

	nLayer, err := NewNoiseLayer(from, to, sender, receiver, forwardDir, ni.Log, ni.keyReg)
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
		buf := make([]aptos.DecodedConsensusMsg, 128)
		n, err := socket.Read(buf)
		if err == io.EOF {
			ni.Log.Printf("EOF reached")
			return
		}
		if err == notConsensusMsgErr {
			ni.Log.Printf("Not a consensus message, forwarding without decoding")
			continue
		}
		if err != nil {
			ni.Log.Printf("Error reading aptos consensus: %v", err)
			return
		}
		buf = buf[:n]
		for _, cmsg := range buf {
			// queue the request in the network manager
			// we only queue successfully decoded consensus messages
			// in order to apply scheduling decisions to them
			// only consensus msgs arrive here
			awaitSendRequest := make(chan struct{})
			networkMsg := &Message{
				Sender:    sender,
				Receiver:  receiver,
				Payload:   cmsg.Msg,
				Type:      Aptos,
				Name:      "Aptos Consensus Message",
				MessageId: ni.NetworkManager.GenerateUniqueId(),
				Send:      awaitSendRequest,
			}

			ni.NetworkManager.Router.QueueMessage(networkMsg)
			<-awaitSendRequest

			err := socket.Write(cmsg)
			if err != nil {
				ni.Log.Printf("Error writing consensus message: %v", err)
			}
			ni.Log.Printf("Forwarded consensus message: node%d->node%d dir=%v sessionId=%d msg=%+v with MessageId=%d\n",
				nLayer.noiseSession.Sender, nLayer.noiseSession.Receiver, nLayer.noiseSession.ForwardDir, nLayer.noiseSession.SessionId, cmsg.Msg,
				networkMsg.MessageId,
			)
		}
	}
}

// Helper functions
// -----------------
func writeFull(conn io.Writer, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			log.Printf("Error writing to connection: %s\n", err.Error())
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
