package aptos

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// One JSON line written by aptos-crypto when byzzfuzz feature flag is enabled
type noiseSecretsLine struct {
	Byzzfuzz     string `json:"byzzfuzz"`
	Event        string `json:"event"`         // "initiator" or "responder"
	RemoteStatic string `json:"remote_static"` // hex
	WriteKey     string `json:"write_key"`     // hex (32 bytes)
	ReadKey      string `json:"read_key"`      // hex (32 bytes)
	WriteNonce0  uint64 `json:"write_nonce0"`
	ReadNonce0   uint64 `json:"read_nonce0"`
}

// Noise session keys
type NoiseSessionKeys struct {
	NodeIndex    int
	Event        string   // initiator/responder
	RemoteStatic [32]byte // public key of peer
	WriteKey     [32]byte
	ReadKey      [32]byte
	WriteNonce0  uint64
	ReadNonce0   uint64
}

// Keyed by (node, event, remote_static)
type sessionKey struct {
	Node  int
	Event string
	RsHex string
}

type KeyRegistry struct {
	baseDir    string
	Mu         sync.RWMutex
	Sessions   map[sessionKey]NoiseSessionKeys // holds the latest keys seen
	filePos    map[int]int64                   // per node file offset for tailing, s.t we don't reread the whole file every time
	nodeStatic map[int]string                  // node index -> static pubkey hex
}

type LinkKeys struct {
	// keys used for decrypt/encrypt depending on direction
	// sender -> receiver direction
	S2R_Initiator NoiseSessionKeys // node=sender,  event=initiator, remote=receiver_static
	R2S_Responder NoiseSessionKeys // node=receiver,event=responder, remote=sender_static

	// receiver -> sender direction
	R2S_Initiator NoiseSessionKeys // node=receiver,event=initiator, remote=sender_static
	S2R_Responder NoiseSessionKeys // node=sender,  event=responder, remote=receiver_static
}

// BASE_DIR : /tmp/aptos-dstest
func NewKeyRegistry(baseDir string) *KeyRegistry {
	return &KeyRegistry{
		baseDir:    baseDir,
		Sessions:   make(map[sessionKey]NoiseSessionKeys),
		filePos:    make(map[int]int64),
		nodeStatic: make(map[int]string),
	}
}

// Builds /tmp/aptos-dstest/nodes/vX/noise_secrets.jsonl
func (kr *KeyRegistry) secretsPath(node int) string {
	return filepath.Join(kr.baseDir, "nodes", fmt.Sprintf("v%d", node), "noise_secrets.jsonl")
}

// Builds /tmp/aptos-dstest/nodes/vX/node_static_key.hex
func (kr *KeyRegistry) nodeStaticPath(node int) string {
	return filepath.Join(kr.baseDir, "nodes", fmt.Sprintf("v%d", node), "node_static_key.hex")
}

// Load once and cache
func (kr *KeyRegistry) GetNodeStaticHex(node int) (string, bool) {
	kr.Mu.RLock()
	if v, ok := kr.nodeStatic[node]; ok {
		kr.Mu.RUnlock()
		return v, true
	}
	kr.Mu.RUnlock()

	b, err := os.ReadFile(kr.nodeStaticPath(node))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if len(s) != 64 { // 32 bytes hex
		return "", false
	}

	kr.Mu.Lock()
	kr.nodeStatic[node] = s
	kr.Mu.Unlock()
	return s, true
}

func (kr *KeyRegistry) GetKeysForLink(sender, receiver int) (LinkKeys, bool) {
	kr.RefreshNode(sender)
	kr.RefreshNode(receiver)

	senderStatic, ok1 := kr.GetNodeStaticHex(sender)
	receiverStatic, ok2 := kr.GetNodeStaticHex(receiver)
	if !ok1 || !ok2 {
		return LinkKeys{}, false
	}

	// sender -> receiver
	s2rInit, ok := kr.Get(sender, "initiator", receiverStatic)
	if !ok {
		return LinkKeys{}, false
	}
	r2sResp, ok := kr.Get(receiver, "responder", senderStatic)
	if !ok {
		return LinkKeys{}, false
	}

	// receiver -> sender
	r2sInit, ok := kr.Get(receiver, "initiator", senderStatic)
	if !ok {
		return LinkKeys{}, false
	}
	s2rResp, ok := kr.Get(sender, "responder", receiverStatic)
	if !ok {
		return LinkKeys{}, false
	}

	return LinkKeys{
		S2R_Initiator: s2rInit,
		R2S_Responder: r2sResp,
		R2S_Initiator: r2sInit,
		S2R_Responder: s2rResp,
	}, true
}

type DialKeys struct {
	S2R_Initiator NoiseSessionKeys // sender, initiator, remote=receiver_static
	R2S_Responder NoiseSessionKeys // receiver, responder, remote=sender_static
}

func (kr *KeyRegistry) GetKeysForDial(sender, receiver int) (DialKeys, bool) {
	_ = kr.RefreshNode(sender)
	_ = kr.RefreshNode(receiver)

	senderStatic, ok1 := kr.GetNodeStaticHex(sender)
	receiverStatic, ok2 := kr.GetNodeStaticHex(receiver)
	if !ok1 || !ok2 {
		return DialKeys{}, false
	}

	s2rInit, ok := kr.Get(sender, "initiator", receiverStatic)
	if !ok {
		return DialKeys{}, false
	}
	r2sResp, ok := kr.Get(receiver, "responder", senderStatic)
	if !ok {
		return DialKeys{}, false
	}

	return DialKeys{S2R_Initiator: s2rInit, R2S_Responder: r2sResp}, true
}

// Tail any new lines since last time for this node
// Due to simultaneous dials/redials, there can be multiple sessions per remote
// We can call this method frequently to gather the latest noise session keys
func (kr *KeyRegistry) RefreshNode(node int) error {
	path := kr.secretsPath(node)

	f, err := os.Open(path)
	if err != nil {
		// File may not exist yet (node hasn't written secrets yet)
		return nil
	}
	defer f.Close()

	kr.Mu.Lock()
	offset := kr.filePos[node]
	kr.Mu.Unlock()

	// Seek to last read offset
	if offset > 0 {
		if _, err := f.Seek(offset, 0); err != nil {
			// If seek fails, restart from 0
			_, _ = f.Seek(0, 0)
			offset = 0
		}
	}

	sc := bufio.NewScanner(f)

	var newOffset int64 = offset

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Track file offset approximately by asking file position after each scan
		// (Scanner doesn't expose it; easiest is to call Seek(0,1) after)
		pos, _ := f.Seek(0, 1)
		newOffset = pos

		if line == "" {
			continue
		}

		var raw noiseSecretsLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if raw.Byzzfuzz != "noise_session" {
			continue
		}
		if raw.Event != "initiator" && raw.Event != "responder" {
			continue
		}

		parsed, ok := decodeSecrets(node, raw)
		if !ok {
			continue
		}

		kr.Mu.Lock()
		kr.Sessions[sessionKey{Node: node, Event: parsed.Event, RsHex: hex.EncodeToString(parsed.RemoteStatic[:])}] = parsed
		kr.filePos[node] = newOffset
		kr.Mu.Unlock()
	}

	// scanner error not fatal
	kr.Mu.Lock()
	kr.filePos[node] = newOffset
	kr.Mu.Unlock()

	return nil
}

func decodeSecrets(node int, raw noiseSecretsLine) (NoiseSessionKeys, bool) {
	var out NoiseSessionKeys
	out.NodeIndex = node
	out.Event = raw.Event
	out.WriteNonce0 = raw.WriteNonce0
	out.ReadNonce0 = raw.ReadNonce0

	rs, err := hex.DecodeString(raw.RemoteStatic)
	if err != nil || len(rs) != 32 {
		return out, false
	}
	copy(out.RemoteStatic[:], rs)

	wk, err := hex.DecodeString(raw.WriteKey)
	if err != nil || len(wk) != 32 {
		return out, false
	}
	copy(out.WriteKey[:], wk)

	rk, err := hex.DecodeString(raw.ReadKey)
	if err != nil || len(rk) != 32 {
		return out, false
	}
	copy(out.ReadKey[:], rk)

	return out, true
}

// Look up latest session keys for (node,event,remote_static_hex)
func (kr *KeyRegistry) Get(node int, event string, remoteStaticHex string) (NoiseSessionKeys, bool) {
	kr.Mu.RLock()
	defer kr.Mu.RUnlock()
	v, ok := kr.Sessions[sessionKey{Node: node, Event: event, RsHex: strings.ToLower(remoteStaticHex)}]
	return v, ok
}

// Returns true if we've seen any secrets for this node yet
func (kr *KeyRegistry) HasAnyForNode(node int) bool {
	kr.Mu.RLock()
	defer kr.Mu.RUnlock()
	for k := range kr.Sessions {
		if k.Node == node {
			return true
		}
	}
	return false
}
