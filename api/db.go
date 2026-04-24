// Package api exposes the public database interface.
//
// Concurrency guarantees (Phase 1):
//   - Write ops (Insert, Update, Delete): exclusive lock — only one writer at a time.
//   - Read ops (Get): shared lock — multiple concurrent readers allowed.
//   - Linearizability per operation: each call is atomic from the caller's perspective.
//   - No snapshot isolation: a Get may observe partial state of a concurrent Insert.
//
// The DB struct must not be copied after first use (contains sync.RWMutex and atomic.Int64).
package api

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"my-non-relational/engine"
)

// DB is the main database handle. Create with Open; close with Close.
type DB struct {
	mu      sync.RWMutex
	store   *engine.HashMap
	counter atomic.Int64 // monotonic counter for ID generation; must not be copied
	path    string       // reserved for Phase 2 (WAL path)
}

// Open initializes an in-memory database.
// In Phase 1, path is stored but not used; Phase 2 will open the WAL at this path.
func Open(path string) (*DB, error) {
	engine.LogInfo("[db] open", "path", path)
	return &DB{
		store: engine.NewHashMap(),
		path:  path,
	}, nil
}

// Insert stores doc in the database and returns its assigned _id.
//
// If doc["_id"] is a non-empty string, it is used as-is.
// Otherwise a unique ID is generated: "<unixNano>-<counter>".
// Returns an error if the _id already exists.
func (db *DB) Insert(doc map[string]any) (string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	id := extractID(doc)
	if id == "" {
		id = fmt.Sprintf("%d-%d", time.Now().UnixNano(), db.counter.Add(1))
	}

	if _, exists := db.store.Get(id); exists {
		engine.LogInfo("[db] insert_error", "reason", "duplicate_id", "id", id)
		return "", fmt.Errorf("duplicate id: %s", id)
	}

	// Store a defensive copy so the caller cannot mutate our internal state.
	stored := copyDoc(doc)
	stored["_id"] = id

	db.store.Set(id, stored)
	engine.LogInfo("[db] insert", "id", id, "fields", len(stored)-1) // -1 excludes _id
	return id, nil
}

// Get retrieves the document with the given id.
// Returns a defensive copy; mutating the result does not affect stored state.
// Returns an error if the id does not exist.
func (db *DB) Get(id string) (map[string]any, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	doc, ok := db.store.Get(id)
	engine.LogInfo("[db] get", "id", id, "found", ok)
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	return copyDoc(doc), nil
}

// Update merges partial into the existing document identified by id.
// Fields in partial overwrite existing fields; fields absent from partial are preserved.
// The _id field cannot be changed; passing a different _id in partial returns an error.
func (db *DB) Update(id string, partial map[string]any) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	existing, ok := db.store.Get(id)
	if !ok {
		engine.LogInfo("[db] update_error", "id", id, "reason", "not_found")
		return fmt.Errorf("not found: %s", id)
	}

	// Guard against _id mutation.
	if newID, ok := partial["_id"]; ok {
		if s, ok := newID.(string); !ok || s != id {
			engine.LogInfo("[db] update_error", "id", id, "reason", "cannot_change_id")
			return fmt.Errorf("cannot change _id")
		}
	}

	// Merge: start from existing, apply partial on top.
	merged := copyDoc(existing)
	mergedFields := 0
	for k, v := range partial {
		if k != "_id" {
			merged[k] = v
			mergedFields++
		}
	}
	merged["_id"] = id // always enforce original _id

	db.store.Set(id, merged)
	engine.LogInfo("[db] update", "id", id, "merged_fields", mergedFields)
	return nil
}

// Delete removes the document with the given id.
// Returns an error if the id does not exist.
func (db *DB) Delete(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	found := db.store.Delete(id)
	engine.LogInfo("[db] delete", "id", id, "found", found)
	if !found {
		return fmt.Errorf("not found: %s", id)
	}
	return nil
}

// Close releases any resources held by the database.
// In Phase 1 this is a no-op; Phase 2 will flush and close the WAL file.
func (db *DB) Close() error {
	engine.LogInfo("[db] close")
	return nil
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
// This prevents callers from mutating the database's internal documents.
func copyDoc(doc map[string]any) map[string]any {
	cp := make(map[string]any, len(doc))
	for k, v := range doc {
		cp[k] = v
	}
	return cp
}
