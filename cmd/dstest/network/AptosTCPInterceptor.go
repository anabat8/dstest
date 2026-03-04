package network

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
)

type AptosTCPInterceptor struct {
	BaseInterceptor
	Listener net.Listener
	keyReg   *KeyRegistry // For keeping track of all sessions keys
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
	ni.keyReg = NewKeyRegistry(baseDir)
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

	ni.Log.Printf("Proxying connection: node%d -> node%d (target %s)\n", sender, receiver, targetAddr)

	// Connect to the target node (forward immediately; the TCP proxy bypasses the scheduler)
	targetConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		ni.Log.Printf("Error connecting to target %s: %s\n", targetAddr, err.Error())
		return
	}
	defer targetConn.Close()

	var dumpOnce sync.Once // Var used for debugging purposes

	// Two-way proxy
	var wg sync.WaitGroup
	wg.Add(2)

	// client -> target
	go func() {
		defer wg.Done()
		ni.session(clientConn, targetConn, sender, receiver, true, &dumpOnce)
	}()

	// target -> client
	go func() {
		defer wg.Done()
		ni.session(targetConn, clientConn, sender, receiver, false, &dumpOnce)
	}()

	wg.Wait()
	ni.Log.Printf("Connection closed: node%d -> node%d\n", sender, receiver)
}

func (ni *AptosTCPInterceptor) session(
	from, to net.Conn,
	sender, receiver int,
	forwardDir bool,
	dumpOnce *sync.Once) {

	framer := NewU16Framer()

	if tcp, ok := to.(*net.TCPConn); ok {
		defer tcp.CloseWrite()
	}

	keysBound := false
	var dk DialKeys
	var nonce uint64

	ni.proxyAndTap(from, to, func(chunk []byte) {

		if !keysBound {
			got, ok := ni.keyReg.GetKeysForDial(sender, receiver)
			if !ok {
				return
			}

			dk = got
			keysBound = true

			// choose which key+nonce we use for this direction:
			// forwardDir=true is sender->receiver, false otherwise
			// sender->receiver uses sender initiator WRITE key + write_nonce0
			// receiver->sender uses receiver responder WRITE key + write_nonce0
			if forwardDir {
				nonce = dk.S2R_Initiator.WriteNonce0
			} else {
				nonce = dk.R2S_Responder.WriteNonce0
			}

			ni.Log.Printf(
				"Bound link node%d<->node%d dir=%v: nonce0=%d",
				sender, receiver, forwardDir, nonce,
			)
			dumpOnce.Do(func() { ni.debugKeysForPair(sender, receiver) })
		}

		// Post-handshake
		// Parse frames and try to decrypt
		frames := framer.Parse(chunk)

		for _, fr := range frames {
			// choose key based on direction
			var key [32]byte
			if forwardDir {
				key = dk.S2R_Initiator.WriteKey
			} else {
				key = dk.R2S_Responder.WriteKey
			}

			ni.Log.Printf("dir=%v key_head=%s nonce0=%d", forwardDir, hex.EncodeToString(key[:])[:16], nonce)

			pt, err := DecryptNoiseFrame(key, nonce, fr)

			curNonce := nonce
			nonce++

			if err != nil {
				ni.Log.Printf("Decrypt failed node%d->node%d dir=%v nonce=%d err=%v head=%s",
					sender, receiver, forwardDir, curNonce, err, headHex(fr, 16))
				continue
			}

			ni.Log.Printf("Decrypted node%d->node%d dir=%v nonce=%d pt_len=%d pt_head=%s",
				sender, receiver, forwardDir, curNonce, len(pt), headHex(pt, 32))
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
	ni.keyReg.mu.RLock()
	defer ni.keyReg.mu.RUnlock()

	ni.Log.Printf("=== KeyRegistry relevant dump for link node%d<->node%d (total=%d) ===",
		sender, receiver, len(ni.keyReg.sessions))

	for k, v := range ni.keyReg.sessions {
		if k.node != sender && k.node != receiver {
			continue
		}
		ni.Log.Printf("node=%d event=%s remote=%s write_nonce0=%d read_nonce0=%d",
			k.node, k.event, k.rsHex[:16], v.WriteNonce0, v.ReadNonce0)
	}
}

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
// U16 framer: extracts frames from a stream: [u16_be len][len bytes of encrypted msg]
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
