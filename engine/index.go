// Package engine — primary and secondary indexes.
//
// # Fase 3 — Índice primario + índice secundario
//
// PrimaryIndex is a sorted slice of (id, offset) pairs with a Bloom Filter
// guard. Before every binary search, MayContain(id) is checked:
//   - false → definite miss, O(1), no binary search
//   - true  → binary search in the sorted slice, O(log N)
//
// Trade-offs:
//   - Sorted slice vs. hash map: O(log N) lookup but supports range queries
//     and ordered iteration. Insertion is O(N) — acceptable for this project.
//   - Bloom Filter cannot support Delete: bits are shared across keys. On
//     Delete only the slice entry is removed; the filter is rebuilt in Phase 9
//     (compaction) or on startup from the live entry set.
//
// SecondaryIndex maps "field:value" → []int64 (WAL offsets). It is used in
// Phase 4's query engine for O(1) equality lookups instead of full scans.
package engine

import (
	"fmt"
)

// ── Primary Index ─────────────────────────────────────────────────────────────

// IndexEntry pairs a document ID with its WAL byte offset.
type IndexEntry struct {
	ID     string
	Offset int64
}

// PrimaryIndex is the in-memory primary index: a sorted slice of IndexEntry
// guarded by a Bloom Filter. Not thread-safe; callers hold db.mu.
type PrimaryIndex struct {
	entries []IndexEntry
	bloom   *BloomFilter
}

// NewPrimaryIndex returns an empty PrimaryIndex sized for expectedN documents.
func NewPrimaryIndex(expectedN uint) *PrimaryIndex {
	if expectedN == 0 {
		expectedN = 1024
	}
	return &PrimaryIndex{
		entries: make([]IndexEntry, 0, expectedN),
		bloom:   NewBloomFilter(expectedN, 0.008), // ~0.8% false positive rate
	}
}

// Add inserts or updates the entry for id with the given WAL offset.
// The slice is kept sorted by ID for binary search. O(N) insertion.
// The Bloom Filter is always updated (it has no delete).
func (p *PrimaryIndex) Add(id string, offset int64) {
	p.bloom.Add(id)

	i := insertionPoint(p.entries, id)

	if i < len(p.entries) && p.entries[i].ID == id {
		// Update existing entry in-place.
		p.entries[i].Offset = offset
		return
	}

	// Insert at position i, shifting elements right.
	p.entries = append(p.entries, IndexEntry{})
	copy(p.entries[i+1:], p.entries[i:])
	p.entries[i] = IndexEntry{ID: id, Offset: offset}
}

// Remove removes the entry for id from the sorted slice.
// The Bloom Filter is NOT updated (it cannot support deletes).
// Returns false if id was not found.
func (p *PrimaryIndex) Remove(id string) bool {
	i := binarySearch(p.entries, id)
	if i == -1 {
		return false
	}
	if i >= len(p.entries) || p.entries[i].ID != id {
		return false
	}
	p.entries = append(p.entries[:i], p.entries[i+1:]...)
	return true
}

// Lookup returns the WAL offset for id.
//
// Pipeline:
//  1. Bloom Filter: if MayContain returns false, id is definitely absent → O(1).
//  2. Binary search in the sorted slice → O(log N).
func (p *PrimaryIndex) Lookup(id string) (int64, bool) {
	if !p.bloom.MayContain(id) {
		LogInfo("[index] lookup", "id", id, "bloom", "miss")
		return 0, false
	}
	i := binarySearch(p.entries, id)
	if i == -1 {
		LogInfo("[index] lookup", "id", id, "bloom", "hit", "found", false)
		return 0, false
	}
	LogInfo("[index] lookup", "id", id, "bloom", "hit", "found", true, "offset", p.entries[i].Offset)
	return p.entries[i].Offset, true
}

// Entries returns the sorted slice. Used for persistence and full scans.
// Callers must not modify the returned slice.
func (p *PrimaryIndex) Entries() []IndexEntry { return p.entries }

// Len returns the number of live entries.
func (p *PrimaryIndex) Len() int { return len(p.entries) }

// RebuildBloom recreates the Bloom Filter from the current live entries.
// Called after a Delete to keep the filter in sync when many entries have
// been removed (prevents excessive false-positive rate). Also called on
// startup when rebuilding the index from the WAL.
func (p *PrimaryIndex) RebuildBloom() {
	n := uint(len(p.entries))
	if n == 0 {
		n = 1
	}
	p.bloom = NewBloomFilter(n, 0.008)
	for _, e := range p.entries {
		p.bloom.Add(e.ID)
	}
}

func binarySearch(entries []IndexEntry, id string) int {
	lo, hi := 0, len(entries)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if entries[mid].ID == id {
			return mid
		} else if entries[mid].ID < id {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return -1
}

func insertionPoint(entries []IndexEntry, id string) int {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if entries[mid].ID < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// ── Secondary Index ───────────────────────────────────────────────────────────

// SecondaryIndex maps "field:value" → []int64 WAL offsets.
// Used for equality lookups in Phase 4's query engine.
// Not thread-safe; callers hold db.mu.
type SecondaryIndex struct {
	m map[string][]int64
}

// NewSecondaryIndex returns an empty SecondaryIndex.
func NewSecondaryIndex() *SecondaryIndex {
	return &SecondaryIndex{m: make(map[string][]int64)}
}

// AddDoc indexes all non-_id fields of doc at the given offset.
func (s *SecondaryIndex) AddDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		key := fmt.Sprintf("%s:%v", field, val)
		s.m[key] = append(s.m[key], offset)
	}
}

// RemoveDoc removes offset from every entry that doc's fields map to.
// Must be called with the OLD document before it is overwritten or deleted.
func (s *SecondaryIndex) RemoveDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		key := fmt.Sprintf("%s:%v", field, val)
		offsets := s.m[key]
		filtered := offsets[:0]
		for _, o := range offsets {
			if o != offset {
				filtered = append(filtered, o)
			}
		}
		if len(filtered) == 0 {
			delete(s.m, key)
		} else {
			s.m[key] = filtered
		}
	}
}

// Lookup returns all WAL offsets for documents where field == value.
func (s *SecondaryIndex) Lookup(field, value string) []int64 {
	key := fmt.Sprintf("%s:%v", field, value)
	return s.m[key]
}

// All returns the internal map for serialization to index.json.
// Callers must not modify the returned map.
func (s *SecondaryIndex) All() map[string][]int64 { return s.m }
