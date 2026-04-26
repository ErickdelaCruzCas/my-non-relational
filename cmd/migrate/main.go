// cmd/migrate — WAL v1 → v2 migration tool
//
// Reads a WAL written with the old 12-byte header (no version byte) and
// rewrites it as a WAL v2 (13-byte header) in the chosen serialization format.
//
// Usage:
//
//	go run ./cmd/migrate -src data/data.log -dst data/data.log.v2 [-format json|msgpack]
//
// Flags:
//
//	-src     path to source WAL (v1, 12-byte header)
//	-dst     path to output WAL (v2, 13-byte header); must not exist
//	-format  serialization format for the output: "json" (default) or "msgpack"
//
// After a successful migration the tool prints the document count found in the
// source WAL and replaces src with dst via an atomic os.Rename.
//
// The tool reads only INSERT/UPDATE/DELETE records and applies them in order,
// keeping the last write per _id. It then writes one INSERT record per live
// document into the new WAL.  This compacts the log (removing superseded and
// deleted documents) as a side effect.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"

	"my-non-relational/engine"
)

func main() {
	src := flag.String("src", "", "source WAL path (required)")
	dst := flag.String("dst", "", "output WAL path (required)")
	format := flag.String("format", "json", "output format: json or msgpack")
	flag.Parse()

	if *src == "" || *dst == "" {
		flag.Usage()
		os.Exit(1)
	}

	var ser engine.Serializer
	var fmtByte byte
	switch *format {
	case "json":
		ser = engine.JSONSerializer{}
		fmtByte = engine.FormatJSON
	case "msgpack":
		ser = engine.MsgPackSerializer{}
		fmtByte = engine.FormatMsgPack
	default:
		log.Fatalf("unknown format %q: use json or msgpack", *format)
	}

	// ── Step 1: replay v1 WAL ─────────────────────────────────────────────────
	docs, err := replayV1(src)
	if err != nil {
		log.Fatalf("replay source WAL: %v", err)
	}
	fmt.Printf("[migrate] source WAL: %d live documents\n", len(docs))

	// ── Step 2: write v2 WAL ──────────────────────────────────────────────────
	wal, err := engine.OpenWAL(*dst, fmtByte)
	if err != nil {
		log.Fatalf("open output WAL: %v", err)
	}
	written := 0
	for _, doc := range docs {
		data, err := ser.Marshal(doc)
		if err != nil {
			wal.Close() //nolint:errcheck
			log.Fatalf("marshal doc %v: %v", doc["_id"], err)
		}
		if _, err := wal.Append(engine.RecordInsert, data); err != nil {
			wal.Close() //nolint:errcheck
			log.Fatalf("write doc %v: %v", doc["_id"], err)
		}
		written++
	}
	if err := wal.Close(); err != nil {
		log.Fatalf("close output WAL: %v", err)
	}
	fmt.Printf("[migrate] output WAL:  %d documents written (format=%s)\n", written, *format)

	// ── Step 3: atomic rename ─────────────────────────────────────────────────
	if err := os.Rename(*dst, *src); err != nil {
		log.Fatalf("rename %s → %s: %v", *dst, *src, err)
	}
	fmt.Printf("[migrate] done: %s replaced atomically\n", *src)
}

// v1Header is the old 12-byte WAL header (no version byte).
const v1HeaderSize = 12

// replayV1 reads a v1 WAL (12-byte header) and returns the live document set.
// It is intentionally minimal — no version dispatch, always JSON, no external deps.
func replayV1(path *string) (map[string]map[string]any, error) {
	f, err := os.Open(*path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]map[string]any), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	docs := make(map[string]map[string]any)

	for {
		var hdr [v1HeaderSize]byte
		_, err := io.ReadFull(f, hdr[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			fmt.Println("[migrate] partial header at end of v1 WAL, stopping")
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}

		size := binary.LittleEndian.Uint32(hdr[0:4])
		recType := binary.LittleEndian.Uint32(hdr[4:8])
		storedCRC := binary.LittleEndian.Uint32(hdr[8:12])

		payload := make([]byte, size)
		_, err = io.ReadFull(f, payload)
		if errors.Is(err, io.ErrUnexpectedEOF) {
			fmt.Println("[migrate] partial payload at end of v1 WAL, stopping")
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read payload: %w", err)
		}

		if crc32.ChecksumIEEE(payload) != storedCRC {
			fmt.Println("[migrate] crc mismatch, skipping record")
			continue
		}

		var doc map[string]any
		if err := json.Unmarshal(payload, &doc); err != nil {
			fmt.Printf("[migrate] json unmarshal error, skipping: %v\n", err)
			continue
		}

		id, _ := doc["_id"].(string)
		if id == "" {
			fmt.Println("[migrate] missing _id, skipping record")
			continue
		}

		switch recType {
		case engine.RecordInsert, engine.RecordUpdate:
			docs[id] = doc
		case engine.RecordDelete:
			delete(docs, id)
		}
	}

	return docs, nil
}
