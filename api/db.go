// Package api exposes the public database interface.
//
// # Concurrency guarantees (Phase 3)
//
//   - Write ops (Insert, Update, Delete): db.mu.Lock() — single writer.
//   - Read ops (Get, Find): db.mu.RLock() — multiple concurrent readers.
//   - WAL ReadAt: no lock needed — pread(2) is concurrency-safe.
//   - Linearizability per operation: each call is atomic from the caller's view.
//   - No snapshot isolation: a Get may observe a concurrent Insert.
//
// # Storage model (Phase 3)
//
// Two modes depending on whether path is set:
//
//	path == "":  in-memory (HashMap). Used by Phase 1 tests. No WAL, no disk.
//	path != "":  disk-backed. Documents live in data/data.log; only the index
//	             (id → offset) lives in RAM. Get reads from disk via ReadAt.
//
// The DB struct must not be copied after first use (contains sync.RWMutex and atomic.Int64).
package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"my-non-relational/engine"
)

// DB is the main database handle. Create with Open; close with Close.
type DB struct {
	mu        sync.RWMutex
	store     *engine.HashMap        // in-memory mode only (path == "")
	index     *engine.PrimaryIndex   // disk mode: id → WAL offset
	secondary *engine.SecondaryIndex // disk mode: "field:value" → []offset
	wal       *engine.WAL            // nil in in-memory mode
	ser       engine.Serializer      // swapped to MsgPackSerializer in Phase 7
	counter   atomic.Int64           // monotonic ID counter; must not be copied
	path      string                 // "" = in-memory, else = data directory
}

// Open initializes the database.
//
// In-memory mode (path == ""): no WAL, no disk I/O. Used by Phase 1 tests.
//
// Disk mode (path != ""):
//  1. Load index.json and spot-check against the WAL (fast path).
//  2. On any failure, rebuild indexes by replaying the WAL (slow path).
//  3. Open the WAL for new writes.
func Open(path string) (*DB, error) {
	engine.LogInfo("[db] open", "path", path)

	db := &DB{
		ser:  engine.JSONSerializer{},
		path: path,
	}

	if path == "" {
		db.store = engine.NewHashMap()
		return db, nil
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	walPath := filepath.Join(path, "data.log")
	indexPath := filepath.Join(path, "index.json")

	db.index = engine.NewPrimaryIndex(1024)
	db.secondary = engine.NewSecondaryIndex()

	// ── Try fast path: load index.json ───────────────────────────────────────
	start := time.Now()
	loaded := false
	if err := db.loadIndex(indexPath); err == nil {
		if err2 := db.spotCheck(walPath); err2 == nil {
			engine.LogInfo("[startup] index loaded from disk",
				"entries", db.index.Len(),
				"elapsed", time.Since(start).Round(time.Millisecond),
			)
			loaded = true
		} else {
			engine.LogInfo("[startup] spot-check failed, rebuilding", "reason", err2)
			db.index = engine.NewPrimaryIndex(1024)
			db.secondary = engine.NewSecondaryIndex()
		}
	} else {
		engine.LogInfo("[startup] index.json missing or corrupt, rebuilding", "reason", err)
	}

	// ── Slow path: rebuild from WAL ───────────────────────────────────────────
	if !loaded {
		if err := db.rebuildFromWAL(walPath); err != nil {
			return nil, fmt.Errorf("rebuild index: %w", err)
		}
		if err := db.saveIndex(indexPath); err != nil {
			engine.LogInfo("[startup] save index failed (non-fatal)", "err", err)
		}
		engine.LogInfo("[startup] index rebuilt from WAL",
			"entries", db.index.Len(),
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
	}

	// ── Open WAL for new writes ───────────────────────────────────────────────
	var err error
	db.wal, err = engine.OpenWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("open wal: %w", err)
	}

	return db, nil
}

// Insert stores doc and returns its assigned _id.
func (db *DB) Insert(doc map[string]any) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	id := extractID(doc)
	if id == "" {
		id = fmt.Sprintf("%d-%d", time.Now().UnixNano(), db.counter.Add(1))
	}

	// ── In-memory mode ────────────────────────────────────────────────────────
	if db.store != nil {
		if _, exists := db.store.Get(id); exists {
			return "", fmt.Errorf("duplicate id: %s", id)
		}
		stored := copyDoc(doc)
		stored["_id"] = id
		db.store.Set(id, stored)
		engine.LogInfo("[db] insert", "id", id, "mode", "memory")
		return id, nil
	}

	// ── Disk mode ─────────────────────────────────────────────────────────────
	if _, exists := db.index.Lookup(id); exists {
		return "", fmt.Errorf("duplicate id: %s", id)
	}

	stored := copyDoc(doc)
	stored["_id"] = id

	data, _ := db.ser.Marshal(stored)
	offset, err := db.wal.Append(engine.RecordInsert, data)
	if err != nil {
		return "", fmt.Errorf("wal append: %w", err)
	}

	db.index.Add(id, offset)
	db.secondary.AddDoc(stored, offset)
	if err := db.saveIndex(filepath.Join(db.path, "index.json")); err != nil {
		engine.LogInfo("[db] save_index_error", "op", "insert", "err", err)
	}

	engine.LogInfo("[db] insert", "id", id, "offset", offset)
	return id, nil
}

// Get retrieves the document with the given id.
// In disk mode: Bloom filter → binary search → ReadAt.
func (db *DB) Get(id string) (map[string]any, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// ── In-memory mode ────────────────────────────────────────────────────────
	if db.store != nil {
		doc, ok := db.store.Get(id)
		engine.LogInfo("[db] get", "id", id, "found", ok, "mode", "memory")
		if !ok {
			return nil, fmt.Errorf("not found: %s", id)
		}
		return copyDoc(doc), nil
	}

	// ── Disk mode ─────────────────────────────────────────────────────────────
	offset, ok := db.index.Lookup(id)
	engine.LogInfo("[db] get", "id", id, "found", ok, "mode", "disk")
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}

	doc, err := engine.ReadDocAt(db.wal.File(), offset, db.ser)
	if err != nil {
		return nil, fmt.Errorf("read doc: %w", err)
	}
	return copyDoc(doc), nil
}

// Update merges partial into the existing document identified by id.
func (db *DB) Update(id string, partial map[string]any) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Guard against _id mutation.
	if newID, ok := partial["_id"]; ok {
		if s, ok := newID.(string); !ok || s != id {
			return fmt.Errorf("cannot change _id")
		}
	}

	// ── In-memory mode ────────────────────────────────────────────────────────
	if db.store != nil {
		existing, ok := db.store.Get(id)
		if !ok {
			return fmt.Errorf("not found: %s", id)
		}
		merged := mergeDoc(existing, partial, id)
		db.store.Set(id, merged)
		engine.LogInfo("[db] update", "id", id, "mode", "memory")
		return nil
	}

	// ── Disk mode ─────────────────────────────────────────────────────────────
	oldOffset, ok := db.index.Lookup(id)
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}

	oldDoc, err := engine.ReadDocAt(db.wal.File(), oldOffset, db.ser)
	if err != nil {
		return fmt.Errorf("read existing doc: %w", err)
	}

	merged := mergeDoc(oldDoc, partial, id)

	data, _ := db.ser.Marshal(merged)
	newOffset, err := db.wal.Append(engine.RecordUpdate, data)
	if err != nil {
		return fmt.Errorf("wal append: %w", err)
	}

	db.secondary.RemoveDoc(oldDoc, oldOffset)
	db.index.Add(id, newOffset)
	db.secondary.AddDoc(merged, newOffset)
	if err := db.saveIndex(filepath.Join(db.path, "index.json")); err != nil {
		engine.LogInfo("[db] save_index_error", "op", "update", "err", err)
	}

	engine.LogInfo("[db] update", "id", id, "old_offset", oldOffset, "new_offset", newOffset)
	return nil
}

// Delete removes the document with the given id.
func (db *DB) Delete(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// ── In-memory mode ────────────────────────────────────────────────────────
	if db.store != nil {
		if !db.store.Delete(id) {
			return fmt.Errorf("not found: %s", id)
		}
		engine.LogInfo("[db] delete", "id", id, "mode", "memory")
		return nil
	}

	// ── Disk mode ─────────────────────────────────────────────────────────────
	offset, ok := db.index.Lookup(id)
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}

	oldDoc, err := engine.ReadDocAt(db.wal.File(), offset, db.ser)
	if err != nil {
		return fmt.Errorf("read doc for delete: %w", err)
	}

	data, _ := db.ser.Marshal(map[string]any{"_id": id})
	if _, err := db.wal.Append(engine.RecordDelete, data); err != nil {
		return fmt.Errorf("wal append: %w", err)
	}

	db.index.Remove(id)
	db.secondary.RemoveDoc(oldDoc, offset)
	if err := db.saveIndex(filepath.Join(db.path, "index.json")); err != nil {
		engine.LogInfo("[db] save_index_error", "op", "delete", "err", err)
	}

	engine.LogInfo("[db] delete", "id", id, "offset", offset)
	return nil
}

// Find executes a query against the database.
//
// In-memory mode: filters inline (no heap, no secondary index).
// Disk mode: delegates to engine.ExecuteFind which selects the strategy
// (secondary index for single eq-filter, full scan otherwise) and applies
// sort/limit via Min-Heap when both are present.
func (db *DB) Find(req engine.FindRequest) ([]map[string]any, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// ── In-memory mode ────────────────────────────────────────────────────────
	if db.store != nil {
		all := db.store.All()
		var out []map[string]any
		for _, doc := range all {
			if engine.MatchesAllFilters(doc, req.Filters) {
				out = append(out, copyDoc(doc))
			}
		}
		engine.LogInfo("[db] find", "mode", "memory", "matched", len(out))
		return out, nil
	}

	// ── Disk mode ─────────────────────────────────────────────────────────────
	return engine.ExecuteFind(db.index.Entries(), db.secondary, db.wal.File(), db.ser, req)
}

// Close saves the index and closes the WAL.
func (db *DB) Close() error {
	engine.LogInfo("[db] close")
	if db.wal != nil {
		if err := db.saveIndex(filepath.Join(db.path, "index.json")); err != nil {
			engine.LogInfo("[db] close_save_index_error", "err", err)
		}
		return db.wal.Close()
	}
	return nil
}

// ── Index persistence ─────────────────────────────────────────────────────────

// indexFile is the JSON structure for data/index.json.
type indexFile struct {
	Primary   [][2]any           `json:"primary"`   // [[id, offset], ...]
	Secondary map[string][]int64 `json:"secondary"` // "field:value" → [offsets]
}

func (db *DB) saveIndex(path string) error {
	entries := db.index.Entries()
	primary := make([][2]any, len(entries))
	for i, e := range entries {
		primary[i] = [2]any{e.ID, e.Offset}
	}
	data, err := json.Marshal(indexFile{
		Primary:   primary,
		Secondary: db.secondary.All(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (db *DB) loadIndex(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f indexFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	for _, pair := range f.Primary {
		id, ok1 := pair[0].(string)
		offsetF, ok2 := pair[1].(float64) // JSON numbers decode as float64
		if !ok1 || !ok2 {
			return fmt.Errorf("invalid primary index entry")
		}
		db.index.Add(id, int64(offsetF))
	}
	for key, offsets := range f.Secondary {
		for _, off := range offsets {
			db.secondary.All()[key] = append(db.secondary.All()[key], off)
		}
	}
	return nil
}

// spotCheck reads the first and last entries of the primary index and
// verifies that the WAL records at those offsets have the expected _id.
func (db *DB) spotCheck(walPath string) error {
	entries := db.index.Entries()
	if len(entries) == 0 {
		return nil
	}
	f, err := os.Open(walPath)
	if err != nil {
		return err
	}
	defer f.Close()

	check := func(e engine.IndexEntry) error {
		doc, err := engine.ReadDocAt(f, e.Offset, db.ser)
		if err != nil {
			return fmt.Errorf("read at %d: %w", e.Offset, err)
		}
		if doc["_id"] != e.ID {
			return fmt.Errorf("id mismatch at offset %d: want %q got %q", e.Offset, e.ID, doc["_id"])
		}
		return nil
	}

	if err := check(entries[0]); err != nil {
		return err
	}
	if len(entries) > 1 {
		if err := check(entries[len(entries)-1]); err != nil {
			return err
		}
	}
	return nil
}

// rebuildFromWAL replays the WAL and rebuilds both indexes from scratch.
func (db *DB) rebuildFromWAL(walPath string) error {
	docs, result, err := engine.ReplayWAL(walPath, db.ser)
	if err != nil {
		return err
	}
	for id, offset := range result.LiveOffsets {
		db.index.Add(id, offset)
	}
	for id, doc := range docs {
		offset := result.LiveOffsets[id]
		db.secondary.AddDoc(doc, offset)
	}
	engine.LogInfo("[startup] wal_rebuild",
		"replayed", result.EntriesReplayed,
		"restored", result.DocsRestored,
	)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// mergeDoc merges partial into existing, enforcing id as the _id.
func mergeDoc(existing, partial map[string]any, id string) map[string]any {
	merged := copyDoc(existing)
	for k, v := range partial {
		if k != "_id" {
			merged[k] = v
		}
	}
	merged["_id"] = id
	return merged
}

// extractID returns doc["_id"] if it is a non-empty string, otherwise "".
func extractID(doc map[string]any) string {
	v, ok := doc["_id"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	return s
}

// copyDoc returns a shallow copy of doc.
func copyDoc(doc map[string]any) map[string]any {
	cp := make(map[string]any, len(doc))
	for k, v := range doc {
		cp[k] = v
	}
	return cp
}
