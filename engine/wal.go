// Package engine — WAL (Write-Ahead Log)
//
// # Fase 7 — WAL v2: byte de versión por registro
//
// Cada registro incluye un byte de versión (byte 12 del header) que indica
// el formato de serialización del payload:
//
//	┌──────────┬──────────┬──────────┬──────────┬──────────────────────────┐
//	│ 4 bytes  │ 4 bytes  │ 4 bytes  │ 1 byte   │ N bytes                  │
//	│  size    │  type    │  crc32   │ version  │  payload                 │
//	│ uint32LE │ uint32LE │ uint32LE │ 1=JSON   │  JSON o MsgPack          │
//	│          │          │          │ 2=msgpk  │                          │
//	└──────────┴──────────┴──────────┴──────────┴──────────────────────────┘
//
// CRC32 cubre únicamente el payload (sin el byte de versión).
// `size` es len(payload) — no incluye el byte de versión.
//
// El byte de versión permite WALs mixtos: si una migración se interrumpe,
// recovery puede leer registros JSON y MsgPack en el mismo archivo porque
// cada registro declara su propio formato.
//
// # Fase 2 — Append-only log con CRC32 (heredado)
//
// Trade-off: file.Sync() en cada escritura garantiza durabilidad.
// ~100× más lento que sin Sync, pero es la garantía más sencilla posible.
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

// Serialization format constants written as the version byte (byte 12).
const (
	FormatJSON    = byte(1)
	FormatMsgPack = byte(2)
)

const headerSize = 13 // size(4) + type(4) + crc32(4) + version(1)

// WAL is an append-only write-ahead log. A single WAL file holds all
// mutations in the order they were applied. It is not safe for concurrent
// use; callers must hold db.mu.Lock() before calling Append.
type WAL struct {
	f      *os.File
	offset int64 // byte position of the next write; maintained locally to avoid Seek calls
	format byte  // FormatJSON or FormatMsgPack — written as version byte in every record
}

// OpenWAL opens (or creates) the WAL file at path.
// format is written as the version byte in every subsequent Append call.
// If the file already exists its current size becomes the starting offset.
func OpenWAL(path string, format byte) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &WAL{f: f, offset: fi.Size(), format: format}, nil
}

// Append writes a single record to the WAL and syncs to disk.
// Returns the byte offset at which the record starts.
//
// Record layout: [size uint32LE][type uint32LE][crc32 uint32LE][version byte][payload…]
// CRC32 is computed over payload only (not the header or version byte).
func (w *WAL) Append(recType uint32, payload []byte) (int64, error) {
	off := w.offset

	crc := crc32.ChecksumIEEE(payload)

	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], recType)
	binary.LittleEndian.PutUint32(hdr[8:12], crc)
	hdr[12] = w.format

	if _, err := w.f.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := w.f.Write(payload); err != nil {
		return 0, err
	}

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
