package crypto

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Default FastCDC boundaries for wire-speed low-power hardware:
// Min 256 KB, Avg 1 MB, Max 4 MB.
const (
	DefaultMinChunkSize = 256 * 1024       // 256 KB
	DefaultAvgChunkSize = 1 * 1024 * 1024  // 1 MB
	DefaultMaxChunkSize = 4 * 1024 * 1024  // 4 MB
)

// GearTable is the 256-element rolling hash lookup table.
var gearTable [256]uint64

func init() {
	// Generate deterministic 64-bit random values using SHA-256 seed
	seed := []byte("StiQRcloud-FastCDC-Gear-Table-v1")
	var buf [32]byte
	h := sha256.Sum256(seed)
	copy(buf[:], h[:])

	for i := 0; i < 256; i++ {
		h = sha256.Sum256(buf[:])
		copy(buf[:], h[:])
		gearTable[i] = binary.BigEndian.Uint64(buf[0:8])
	}
}

// ChunkConfig holds FastCDC sizing parameters.
type ChunkConfig struct {
	MinSize int `json:"min_size"`
	AvgSize int `json:"avg_size"`
	MaxSize int `json:"max_size"`
}

// DefaultChunkConfig returns production defaults (256 KB min, 1 MB avg, 4 MB max).
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		MinSize: DefaultMinChunkSize,
		AvgSize: DefaultAvgChunkSize,
		MaxSize: DefaultMaxChunkSize,
	}
}

// RawChunk represents an unencrypted data chunk identified by content offset and hash.
type RawChunk struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	Hash   string `json:"hash"` // Plaintext SHA-256 hex
	Data   []byte `json:"-"`
}

// FastCDC splits data into content-defined variable chunks using Gear rolling hash.
// Content edits in the middle of a file only alter local chunks and prevent boundary domino shifts.
func FastCDC(data []byte, cfg ChunkConfig) ([]RawChunk, error) {
	if cfg.MinSize <= 0 || cfg.AvgSize <= cfg.MinSize || cfg.MaxSize <= cfg.AvgSize {
		return nil, errors.New("invalid chunk config: must satisfy 0 < MinSize < AvgSize < MaxSize")
	}

	dataLen := len(data)
	if dataLen == 0 {
		return nil, nil
	}

	// Compute bit masks for normalized sub-chunk chunking:
	// MaskS (smaller, stricter) before AvgSize; MaskL (larger, looser) after AvgSize.
	bits := log2(cfg.AvgSize)
	maskS := uint64((1 << (bits + 1)) - 1)
	maskL := uint64((1 << (bits - 1)) - 1)

	var chunks []RawChunk
	cursor := 0
	chunkIndex := 0

	for cursor < dataLen {
		remaining := dataLen - cursor
		if remaining <= cfg.MinSize {
			// Remaining bytes fit in a final chunk
			chunkData := data[cursor:]
			chunks = append(chunks, RawChunk{
				Index:  chunkIndex,
				Offset: int64(cursor),
				Length: len(chunkData),
				Hash:   HashBytes(chunkData),
				Data:   chunkData,
			})
			break
		}

		// Fast-forward past MinSize
		normalMax := cfg.MaxSize
		if remaining < normalMax {
			normalMax = remaining
		}

		var fingerPrint uint64

		cutPoint := normalMax
		for i := cfg.MinSize; i < normalMax; i++ {
			b := data[cursor+i]
			fingerPrint = (fingerPrint << 1) + gearTable[b]

			if i < cfg.AvgSize {
				if (fingerPrint & maskS) == 0 {
					cutPoint = i + 1
					break
				}
			} else {
				if (fingerPrint & maskL) == 0 {
					cutPoint = i + 1
					break
				}
			}
		}

		chunkData := data[cursor : cursor+cutPoint]
		chunks = append(chunks, RawChunk{
			Index:  chunkIndex,
			Offset: int64(cursor),
			Length: len(chunkData),
			Hash:   HashBytes(chunkData),
			Data:   chunkData,
		})

		cursor += cutPoint
		chunkIndex++
	}

	return chunks, nil
}

func log2(n int) int {
	bits := 0
	for n > 1 {
		n >>= 1
		bits++
	}
	return bits
}
