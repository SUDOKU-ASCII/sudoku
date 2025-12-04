// pkg/obfs/sudoku/conn.go
package sudoku

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	crypto_rand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
)

const IOBufferSize = 32 * 1024

type Conn struct {
	net.Conn
	table      *Table
	reader     *bufio.Reader
	recorder   *bytes.Buffer
	recording  bool
	recordLock sync.Mutex

	rawBuf      []byte
	pendingData []byte
	hintBuf     []byte

	rng         *rand.Rand
	paddingRate float32

	// High bandwidth (downlink) mode
	boostWrite bool
	boostRead  bool
	boostASCII bool
	boostEnc   cipher.Stream
	boostDec   cipher.Stream
	encBitBuf  uint64
	encBits    int
	decBitBuf  uint64
	decBits    int
	boostMu    sync.Mutex
}

func NewConn(c net.Conn, table *Table, pMin, pMax int, record bool) *Conn {
	var seedBytes [8]byte
	if _, err := crypto_rand.Read(seedBytes[:]); err != nil {
		binary.BigEndian.PutUint64(seedBytes[:], uint64(rand.Int63()))
	}
	seed := int64(binary.BigEndian.Uint64(seedBytes[:]))
	localRng := rand.New(rand.NewSource(seed))

	min := float32(pMin) / 100.0
	rng := float32(pMax-pMin) / 100.0
	rate := min + localRng.Float32()*rng

	sc := &Conn{
		Conn:        c,
		table:       table,
		reader:      bufio.NewReaderSize(c, IOBufferSize),
		rawBuf:      make([]byte, IOBufferSize),
		pendingData: make([]byte, 0, 4096),
		hintBuf:     make([]byte, 0, 4),
		rng:         localRng,
		paddingRate: rate,
	}
	if record {
		sc.recorder = new(bytes.Buffer)
		sc.recording = true
	}
	return sc
}

// EnableBoost activates the high-bandwidth downlink codec.
// write/read toggles control which direction uses the codec on this side.
func (sc *Conn) EnableBoost(write, read bool, aesKey, iv []byte, isASCII bool) error {
	if len(aesKey) < 16 {
		return fmt.Errorf("aesKey too short")
	}
	if len(iv) < aes.BlockSize {
		return fmt.Errorf("iv too short")
	}

	block, err := aes.NewCipher(aesKey[:aes.BlockSize])
	if err != nil {
		return err
	}

	if write {
		sc.boostMu.Lock()
		sc.boostEnc = cipher.NewCTR(block, iv[:aes.BlockSize])
		sc.encBitBuf = 0
		sc.encBits = 0
		sc.boostWrite = true
		sc.boostASCII = isASCII
		sc.boostMu.Unlock()
	}
	if read {
		sc.boostDec = cipher.NewCTR(block, iv[:aes.BlockSize])
		sc.decBitBuf = 0
		sc.decBits = 0
		sc.boostRead = true
		sc.boostASCII = isASCII
		// Reset pending hint buffers to avoid mixing modes.
		sc.hintBuf = sc.hintBuf[:0]
	}
	return nil
}

func (sc *Conn) Close() error {
	if sc.boostWrite {
		_ = sc.flushBoostPadding()
	}
	if sc.Conn == nil {
		return nil
	}
	return sc.Conn.Close()
}

func (sc *Conn) StopRecording() {
	sc.recordLock.Lock()
	sc.recording = false
	sc.recorder = nil
	sc.recordLock.Unlock()
}

func (sc *Conn) GetBufferedAndRecorded() []byte {
	if sc == nil {
		return nil
	}

	sc.recordLock.Lock()
	defer sc.recordLock.Unlock()

	var recorded []byte
	if sc.recorder != nil {
		recorded = sc.recorder.Bytes()
	}

	buffered := sc.reader.Buffered()
	if buffered > 0 {
		peeked, _ := sc.reader.Peek(buffered)
		full := make([]byte, len(recorded)+len(peeked))
		copy(full, recorded)
		copy(full[len(recorded):], peeked)
		return full
	}
	return recorded
}

func (sc *Conn) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	if sc.boostWrite {
		return sc.writeBoost(p)
	}

	outCapacity := len(p) * 6
	out := make([]byte, 0, outCapacity)
	pads := sc.table.PaddingPool
	padLen := len(pads)

	for _, b := range p {
		if sc.rng.Float32() < sc.paddingRate {
			out = append(out, pads[sc.rng.Intn(padLen)])
		}

		puzzles := sc.table.EncodeTable[b]
		puzzle := puzzles[sc.rng.Intn(len(puzzles))]

		// Shuffle hints
		perm := []int{0, 1, 2, 3}
		sc.rng.Shuffle(4, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

		for _, idx := range perm {
			if sc.rng.Float32() < sc.paddingRate {
				out = append(out, pads[sc.rng.Intn(padLen)])
			}
			out = append(out, puzzle[idx])
		}
	}

	if sc.rng.Float32() < sc.paddingRate {
		out = append(out, pads[sc.rng.Intn(padLen)])
	}

	_, err = sc.Conn.Write(out)
	return len(p), err
}

func (sc *Conn) Read(p []byte) (n int, err error) {
	if sc.boostRead {
		return sc.readBoost(p)
	}

	if len(sc.pendingData) > 0 {
		n = copy(p, sc.pendingData)
		if n == len(sc.pendingData) {
			sc.pendingData = sc.pendingData[:0]
		} else {
			sc.pendingData = sc.pendingData[n:]
		}
		return n, nil
	}

	for {
		if len(sc.pendingData) > 0 {
			break
		}

		nr, rErr := sc.reader.Read(sc.rawBuf)
		if nr > 0 {
			chunk := sc.rawBuf[:nr]
			sc.recordLock.Lock()
			if sc.recording {
				sc.recorder.Write(chunk)
			}
			sc.recordLock.Unlock()

			for _, b := range chunk {
				isPadding := false

				if sc.table.IsASCII {
					// === ASCII Mode ===
					// Padding: 001xxxxx (Bit 6 is 0) -> (b & 0x40) == 0
					// Hint:    01vvpppp (Bit 6 is 1) -> (b & 0x40) != 0
					if (b & 0x40) == 0 {
						isPadding = true
					}
				} else {
					// === Entropy Mode ===
					// Padding: 0x80... or 0x10... -> (b & 0x90) != 0
					if (b & 0x90) != 0 {
						isPadding = true
					}
				}

				if isPadding {
					continue
				}

				sc.hintBuf = append(sc.hintBuf, b)
				if len(sc.hintBuf) == 4 {
					key := packHintsToKey([4]byte{sc.hintBuf[0], sc.hintBuf[1], sc.hintBuf[2], sc.hintBuf[3]})
					val, ok := sc.table.DecodeMap[key]
					if !ok {
						return 0, errors.New("INVALID_SUDOKU_MAP_MISS")
					}
					sc.pendingData = append(sc.pendingData, val)
					sc.hintBuf = sc.hintBuf[:0]
				}
			}
		}

		if rErr != nil {
			return 0, rErr
		}
		if len(sc.pendingData) > 0 {
			break
		}
	}

	n = copy(p, sc.pendingData)
	if n == len(sc.pendingData) {
		sc.pendingData = sc.pendingData[:0]
	} else {
		sc.pendingData = sc.pendingData[n:]
	}
	return n, nil
}

func (sc *Conn) packBoostByte(bits byte) byte {
	if sc.boostASCII {
		return 0x40 | (bits & 0x3F)
	}
	high := (bits & 0x30) << 1
	low := bits & 0x0F
	return high | low
}

func (sc *Conn) unpackBoostByte(b byte) byte {
	if sc.boostASCII {
		return b & 0x3F
	}
	return ((b & 0x60) >> 1) | (b & 0x0F)
}

func (sc *Conn) writeBoost(p []byte) (int, error) {
	if sc.boostEnc == nil {
		return 0, errors.New("boost encoder not initialized")
	}

	encBuf := make([]byte, len(p))
	sc.boostEnc.XORKeyStream(encBuf, p)

	pads := sc.table.PaddingPool
	padLen := len(pads)

	out := make([]byte, 0, len(p)*2)

	sc.boostMu.Lock()
	for _, b := range encBuf {
		sc.encBitBuf = (sc.encBitBuf << 8) | uint64(b)
		sc.encBits += 8

		for sc.encBits >= 6 {
			sc.encBits -= 6
			chunk := byte(sc.encBitBuf>>sc.encBits) & 0x3F
			encoded := sc.packBoostByte(chunk)

			if sc.rng.Float32() < sc.paddingRate {
				out = append(out, pads[sc.rng.Intn(padLen)])
			}
			out = append(out, encoded)

			if sc.encBits == 0 {
				sc.encBitBuf = 0
			} else {
				sc.encBitBuf = sc.encBitBuf & ((1 << sc.encBits) - 1)
			}
		}
	}
	sc.boostMu.Unlock()

	if sc.rng.Float32() < sc.paddingRate {
		out = append(out, pads[sc.rng.Intn(padLen)])
	}

	_, err := sc.Conn.Write(out)
	return len(p), err
}

func (sc *Conn) readBoost(p []byte) (int, error) {
	if len(sc.pendingData) > 0 {
		n := copy(p, sc.pendingData)
		if n == len(sc.pendingData) {
			sc.pendingData = sc.pendingData[:0]
		} else {
			sc.pendingData = sc.pendingData[n:]
		}
		return n, nil
	}

	for {
		if len(sc.pendingData) > 0 {
			break
		}

		nr, rErr := sc.reader.Read(sc.rawBuf)
		if nr > 0 {
			chunk := sc.rawBuf[:nr]
			sc.recordLock.Lock()
			if sc.recording {
				sc.recorder.Write(chunk)
			}
			sc.recordLock.Unlock()

			for _, b := range chunk {
				isPadding := false

				if sc.boostASCII {
					if (b & 0x40) == 0 {
						isPadding = true
					}
				} else {
					if (b & 0x90) != 0 {
						isPadding = true
					}
				}

				if isPadding {
					continue
				}

				chunkBits := sc.unpackBoostByte(b)
				sc.decBitBuf = (sc.decBitBuf << 6) | uint64(chunkBits)
				sc.decBits += 6

				for sc.decBits >= 8 {
					sc.decBits -= 8
					byteVal := byte(sc.decBitBuf >> sc.decBits)
					if sc.decBits == 0 {
						sc.decBitBuf = 0
					} else {
						sc.decBitBuf = sc.decBitBuf & ((1 << sc.decBits) - 1)
					}

					tmp := []byte{byteVal}
					if sc.boostDec == nil {
						return 0, errors.New("boost decoder missing")
					}
					sc.boostDec.XORKeyStream(tmp, tmp)
					sc.pendingData = append(sc.pendingData, tmp[0])
				}
			}
		}

		if rErr != nil {
			return 0, rErr
		}
		if len(sc.pendingData) > 0 {
			break
		}
	}

	n := copy(p, sc.pendingData)
	if n == len(sc.pendingData) {
		sc.pendingData = sc.pendingData[:0]
	} else {
		sc.pendingData = sc.pendingData[n:]
	}
	return n, nil
}

// flushBoostPadding emits leftover bits (zero padded) to finish the stream.
func (sc *Conn) flushBoostPadding() error {
	sc.boostMu.Lock()
	defer sc.boostMu.Unlock()

	if !sc.boostWrite || sc.encBits == 0 {
		return nil
	}

	pads := sc.table.PaddingPool
	padLen := len(pads)

	remaining := byte((sc.encBitBuf << (6 - sc.encBits)) & 0x3F)
	encoded := sc.packBoostByte(remaining)

	out := make([]byte, 0, 2)
	if sc.rng.Float32() < sc.paddingRate {
		out = append(out, pads[sc.rng.Intn(padLen)])
	}
	out = append(out, encoded)

	if sc.rng.Float32() < sc.paddingRate {
		out = append(out, pads[sc.rng.Intn(padLen)])
	}

	sc.encBitBuf = 0
	sc.encBits = 0

	_, err := sc.Conn.Write(out)
	return err
}
