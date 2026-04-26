// Package engine — WAL recovery
//
// # Fase 7 — Replay del WAL v2: despacho por byte de versión
//
// ReplayWAL acepta un mapa de serializadores (map[byte]Serializer) en lugar
// de uno solo. Cada registro declara su propio formato en el byte 12 del header
// (FormatJSON=1, FormatMsgPack=2). Esto permite WALs mixtos: si una migración
// se interrumpió, recovery puede leer registros JSON y MsgPack en el mismo archivo.
//
// Fallback: si el byte de versión no tiene entrada en el mapa, se usa
// sers[FormatJSON] para compatibilidad con WALs v1 sin byte de versión
// (que escriben 0x00 en esa posición).
//
// # Fase 2 — Replay del WAL con validación CRC32 (heredado)
//
// ReplayWAL lee data/data.log de inicio a fin y reconstruye el estado vivo
// de la base de datos. El último registro por _id gana: un UPDATE sobreescribe
// un INSERT anterior; un DELETE elimina el documento del mapa.
//
// Tolerancia a fallos:
//   - CRC inválido → registro descartado con warning. Nunca causa panic.
//   - Escritura parcial al final del log (crash durante Append) → io.ErrUnexpectedEOF
//     en el payload se trata como fin normal, no como error fatal.
//   - Registro con JSON inválido → descartado con warning.
package engine

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

// RecoveryResult holds stats from a WAL replay, printed at startup.
type RecoveryResult struct {
	EntriesReplayed int
	DocsRestored    int
	LiveOffsets     map[string]int64 // id → WAL offset of the last live INSERT/UPDATE record
}

// ReplayWAL reads the WAL at path and returns the live document set.
//
// sers maps each format byte (FormatJSON=1, FormatMsgPack=2) to its Serializer.
// Each record's version byte (hdr[12]) selects the deserializer for that record,
// enabling transparent recovery of mixed JSON+MsgPack WALs. If a version byte
// is not in sers, falls back to sers[FormatJSON].
//
// If the file does not exist (first startup), it returns an empty map and
// a zero RecoveryResult — not an error.
//
// The returned map is keyed by _id. Callers load it into the HashMap.
func ReplayWAL(path string, sers map[byte]Serializer) (map[string]map[string]any, RecoveryResult, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]map[string]any), RecoveryResult{}, nil
	}
	if err != nil {
		return nil, RecoveryResult{}, err
	}
	defer f.Close()

	docs := make(map[string]map[string]any)
	result := RecoveryResult{LiveOffsets: make(map[string]int64)}
	var pos int64 // byte position of the current record's start

	for {
		// ── Read 13-byte header (WAL v2) ─────────────────────────────────
		// Layout: size(4) + type(4) + crc32(4) + version(1)
		recordOffset := pos
		var hdr [headerSize]byte
		_, err := io.ReadFull(f, hdr[:])
		if errors.Is(err, io.EOF) {
			// Clean end: no bytes read for this record.
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Partial header: crash happened while writing the header itself.
			LogInfo("[recovery] partial_header at end of log, stopping")
			break
		}
		if err != nil {
			return nil, result, err
		}

		size := binary.LittleEndian.Uint32(hdr[0:4])
		recType := binary.LittleEndian.Uint32(hdr[4:8])
		storedCRC := binary.LittleEndian.Uint32(hdr[8:12])
		version := hdr[12] // FormatJSON=1, FormatMsgPack=2; 0x00 for v1 WALs

		// ── Read payload ─────────────────────────────────────────────────
		payload := make([]byte, size)
		_, err = io.ReadFull(f, payload)
		pos += int64(headerSize) + int64(size)
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Partial payload: crash happened mid-write. Treat as end of log.
			LogInfo("[recovery] partial_payload at end of log, stopping")
			break
		}
		if err != nil {
			return nil, result, err
		}

		result.EntriesReplayed++

		// ── Validate CRC32 ───────────────────────────────────────────────
		if crc32.ChecksumIEEE(payload) != storedCRC {
			LogInfo("[recovery] crc_mismatch, skipping record",
				"entry", result.EntriesReplayed)
			continue
		}

		// ── Select serializer by version byte ────────────────────────────
		ser, ok := sers[version]
		if !ok {
			// Graceful fallback: v1 WALs have version=0x00; unknown versions
			// default to JSON for forward compatibility.
			ser = sers[FormatJSON]
		}

		// ── Deserialize payload ──────────────────────────────────────────
		var doc map[string]any
		if err := ser.Unmarshal(payload, &doc); err != nil {
			LogInfo("[recovery] unmarshal_error, skipping record",
				"entry", result.EntriesReplayed, "err", err)
			continue
		}

		id, _ := doc["_id"].(string)
		if id == "" {
			LogInfo("[recovery] missing_id, skipping record",
				"entry", result.EntriesReplayed)
			continue
		}

		// ── Apply to state ───────────────────────────────────────────────
		switch recType {
		case RecordInsert, RecordUpdate:
			docs[id] = doc
			result.LiveOffsets[id] = recordOffset
		case RecordDelete:
			delete(docs, id)
			delete(result.LiveOffsets, id)
		default:
			LogInfo("[recovery] unknown_record_type, skipping",
				"type", recType, "entry", result.EntriesReplayed)
		}
	}

	result.DocsRestored = len(docs)
	return docs, result, nil
}
