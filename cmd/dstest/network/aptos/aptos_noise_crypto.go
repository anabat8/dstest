package aptos

//! Aptos:
//! For post-handshake NoiseStream, each direction is:
//! 	* framed as [u16_be len][len bytes of (ciphertext||tag)]
//! 	* encrypted with AES-256-GCM using:
//! 		AAD empty
//! 		nonce = 00000000 || u64_be(counter)
//!			counter increments per frame in that direction

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
)

type NoiseSession struct {
	keys         DialKeys
	aesKey       [32]byte
	decryptNonce uint64
	encryptNonce uint64
	ForwardDir   bool
	Sender       int
	Receiver     int
	SessionId    int64
}

func NewNoiseSession(
	dk DialKeys,
	aesKey [32]byte,
	nonce uint64,
	forwardDir bool,
	sender int,
	receiver int,
	sessionId int64,
) *NoiseSession {
	return &NoiseSession{
		keys:         dk,
		aesKey:       aesKey,
		decryptNonce: nonce,
		encryptNonce: nonce,
		ForwardDir:   forwardDir,
		Sender:       sender,
		Receiver:     receiver,
		SessionId:    sessionId,
	}
}

var ErrCiphertextTooShort = errors.New("ciphertext too short")

// Aptos nonce format: 12 bytes = 4 zero bytes || u64 big-endian counter
func aptosNonce(n uint64) []byte {
	nonce := make([]byte, 12)
	// first 4 bytes are already 0
	binary.BigEndian.PutUint64(nonce[4:], n)
	return nonce
}

// DecryptNoiseFrame decrypts a *single NoiseStream frame payload*:
// frame = ciphertext||tag  (length already stripped by U16 framer)
// tag size: 16 bytes
// AAD is empty (post-handshake in Aptos NoiseStream).
func (ns *NoiseSession) DecryptNoiseFrame(frame []byte) ([]byte, uint64, error) {
	if len(frame) < 16 {
		return nil, 0, ErrCiphertextTooShort
	}

	block, err := aes.NewCipher(ns.aesKey[:])
	if err != nil {
		return nil, 0, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}

	// empty AAD
	// gcm.Open already expects frame to be = ciphertext||tag and
	// returns plaintext only
	// it already authenticates and removes the tag for us
	pt, err := gcm.Open(nil, aptosNonce(ns.decryptNonce), frame, nil)
	if err != nil {
		return nil, 0, err
	}

	usedNonce := ns.decryptNonce
	ns.decryptNonce++
	return pt, usedNonce, nil
}

func (ns *NoiseSession) EncryptNoiseFrame(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(ns.aesKey[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// empty AAD
	ct := gcm.Seal(nil, aptosNonce(ns.encryptNonce), plaintext, nil)
	ns.encryptNonce++
	return ct, nil
}
