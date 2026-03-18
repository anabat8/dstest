package aptos

import "encoding/binary"

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
