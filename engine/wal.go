// Package engine — WAL (Write-Ahead Log)
//
// # Fase 2 — Append-only log con CRC32
//
// Cada operación de escritura (INSERT, UPDATE, DELETE) se serializa en
// data/data.log como un registro autocontenido:
//
//	┌──────────┬──────────┬──────────┬──────────────────────────┐
//	│ 4 bytes  │ 4 bytes  │ 4 bytes  │ N bytes                  │
//	│  size    │  type    │  crc32   │  JSON payload             │
//	│ uint32LE │ uint32LE │ uint32LE │  {"_id":"...", ...}       │
//	└──────────┴──────────┴──────────┴──────────────────────────┘
//
// El CRC32 protege el payload contra corrupción silenciosa en disco.
// Recovery rechaza registros con checksum inválido en lugar de insertar
// datos corruptos silenciosamente.
//
// Trade-off: file.Sync() en cada escritura garantiza que el SO confirma
// la escritura antes de retornar al cliente. Esto es ~100x más lento que
// no hacerlo, pero es la garantía de durabilidad más sencilla posible.
// Se medirá en Fase 8 (observabilidad).
package engine

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

// Record type constants written into the WAL header.
const (
	RecordInsert uint32 = 1
	RecordUpdate uint32 = 2
	RecordDelete uint32 = 3
)

const headerSize = 12 // 3 × uint32LE

// WAL is an append-only write-ahead log. A single WAL file holds all
// mutations in the order they were applied. It is not safe for concurrent
// use; callers must hold db.mu.Lock() before calling Append.
type WAL struct {
	f      *os.File
	offset int64 // byte position of the next write; maintained locally to avoid Seek calls
}

// OpenWAL opens (or creates) the WAL file at path.
// If the file already exists its current size becomes the starting offset,
// so subsequent Appends land after all existing records.
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &WAL{f: f, offset: fi.Size()}, nil
}

// Append writes a single record to the WAL and syncs to disk.
// Returns the byte offset at which the record starts — reserved for Fase 3
// (index stores id → offset for O(1) disk reads).
//
// The record layout: [size uint32LE][type uint32LE][crc32 uint32LE][payload…]
// CRC32 is computed over payload only (not the header).
func (w *WAL) Append(recType uint32, payload []byte) (int64, error) {
	off := w.offset

	crc := crc32.ChecksumIEEE(payload)

	// Pack header into a fixed-size array: one Write, no heap alloc.
	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], recType)
	binary.LittleEndian.PutUint32(hdr[8:12], crc)

	if _, err := w.f.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := w.f.Write(payload); err != nil {
		return 0, err
	}

	// Sync before returning so the caller knows the record is on stable storage.
	if err := w.f.Sync(); err != nil {
		return 0, err
	}

	w.offset += int64(headerSize + len(payload))
	return off, nil
}

// File returns the underlying *os.File.
// Callers may use ReadAt for concurrent random reads without holding any lock
// because ReadAt maps to pread(2), which is safe for concurrent use.
// Do NOT call Read/Write/Seek on the returned file directly.
func (w *WAL) File() *os.File { return w.f }

// Close syncs any buffered writes and closes the file.
func (w *WAL) Close() error {
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}
