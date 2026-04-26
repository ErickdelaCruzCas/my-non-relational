// Package engine — storage: random-access reads from the WAL file.
//
// # Fase 3 — ReadDocAt
//
// In Phase 1-2 documents lived in the HashMap in RAM. Phase 3 moves them to
// disk: only the primary index (id → offset) stays in memory. ReadDocAt reads
// a single WAL record by its byte offset and deserializes it.
//
// f.ReadAt maps to pread(2) on POSIX systems, which is safe for concurrent
// use without holding any lock. Multiple goroutines can call ReadDocAt
// simultaneously on the same *os.File as long as they use different offsets.
package engine

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// ReadDocAt reads the WAL record starting at offset in f and returns the
// deserialized document. It validates the CRC32 checksum and returns an
// error if the record is corrupt.
//
// The record layout expected at offset:
//
//	[size uint32LE][type uint32LE][crc32 uint32LE][payload…]
func ReadDocAt(f interface {
	ReadAt(b []byte, off int64) (int, error)
}, offset int64, ser Serializer) (map[string]any, error) {
	// ── Read 12-byte header ──────────────────────────────────────────────────
	var hdr [headerSize]byte
	if _, err := f.ReadAt(hdr[:], offset); err != nil {
		return nil, fmt.Errorf("storage: read header at %d: %w", offset, err)
	}

	size := binary.LittleEndian.Uint32(hdr[0:4])
	// hdr[4:8] is recType — not validated here; callers trust the index
	storedCRC := binary.LittleEndian.Uint32(hdr[8:12])

	// ── Read payload ─────────────────────────────────────────────────────────
	payload := make([]byte, size)
	if _, err := f.ReadAt(payload, offset+int64(headerSize)); err != nil {
		return nil, fmt.Errorf("storage: read payload at %d: %w", offset, err)
	}

	// ── Validate CRC32 ───────────────────────────────────────────────────────
	if crc32.ChecksumIEEE(payload) != storedCRC {
		return nil, fmt.Errorf("storage: crc mismatch at offset %d", offset)
	}

	// ── Deserialize ──────────────────────────────────────────────────────────
	var doc map[string]any
	if err := ser.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("storage: unmarshal at offset %d: %w", offset, err)
	}
	return doc, nil
}
