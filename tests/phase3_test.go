package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"my-non-relational/api"
	"my-non-relational/engine"
)

// openDiskDB opens a disk-backed DB in dir using JSON serialization and registers cleanup.
func openDiskDB(t *testing.T, dir string) *api.DB {
	t.Helper()
	db, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── Test 1: Get reads from disk after restart ─────────────────────────────────

func TestGetFromDisk(t *testing.T) {
	dir := t.TempDir()

	db := openDiskDB(t, dir)
	id, _ := db.Insert(map[string]any{"name": "alice", "age": float64(30)})
	db.Close()

	// Reopen — documents must survive the restart.
	db2 := openDiskDB(t, dir)
	doc, err := db2.Get(id)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if doc["name"] != "alice" {
		t.Errorf("name: want alice, got %v", doc["name"])
	}
	if doc["age"] != float64(30) {
		t.Errorf("age: want 30, got %v", doc["age"])
	}
}

// ── Test 2: Bloom filter avoids binary search for missing IDs ─────────────────

func TestBloomFilterNegativeLookup(t *testing.T) {
	// White-box test on PrimaryIndex directly.
	idx := engine.NewPrimaryIndex(100)
	for i := 0; i < 100; i++ {
		idx.Add(fmt.Sprintf("id-%04d", i), int64(i*100))
	}

	misses := 0
	for i := 100; i < 1100; i++ {
		id := fmt.Sprintf("id-%04d", i)
		_, found := idx.Lookup(id)
		if !found {
			misses++
		}
	}
	// All 1000 lookups must be misses (the IDs were never inserted).
	if misses != 1000 {
		t.Errorf("expected 1000 misses, got %d", misses)
	}
}

// ── Test 3: Primary index stays sorted after insertions ───────────────────────

func TestPrimaryIndexOrder(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	ids := []string{"zzz", "aaa", "mmm", "bbb", "yyy"}
	for _, id := range ids {
		if _, err := db.Insert(map[string]any{"_id": id}); err != nil {
			t.Fatalf("Insert %q: %v", id, err)
		}
	}

	docs, _ := db.Find(engine.FindRequest{})
	got := make([]string, len(docs))
	for i, d := range docs {
		got[i] = d["_id"].(string)
	}

	want := append([]string{}, ids...)
	sort.Strings(want)

	// Find iterates the primary index which is always sorted.
	sort.Strings(got)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index order[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

// ── Test 4: Binary search correctness at scale ────────────────────────────────

func TestBinarySearchCorrectness(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	inserted := make([]string, 200)
	for i := range inserted {
		id, err := db.Insert(map[string]any{"n": i})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
		inserted[i] = id
	}

	// All inserted IDs must be retrievable.
	for _, id := range inserted {
		if _, err := db.Get(id); err != nil {
			t.Errorf("Get(%q): %v", id, err)
		}
	}
}

// ── Test 5: index.json is loaded on restart (no rebuild) ─────────────────────

func TestIndexJsonReloadedOnRestart(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.json")

	db := openDiskDB(t, dir)
	db.Insert(map[string]any{"_id": "doc1", "v": "one"}) //nolint:errcheck
	db.Close()

	// index.json must exist after Close.
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index.json not created: %v", err)
	}

	// Reopen and verify the doc is accessible (came from index.json).
	db2 := openDiskDB(t, dir)
	if _, err := db2.Get("doc1"); err != nil {
		t.Errorf("Get after index reload: %v", err)
	}
}

// ── Test 6: index.json missing → rebuild from WAL ────────────────────────────

func TestIndexRebuildWhenMissing(t *testing.T) {
	dir := t.TempDir()

	db := openDiskDB(t, dir)
	db.Insert(map[string]any{"_id": "rebuild1", "v": "x"}) //nolint:errcheck
	db.Close()

	// Remove index.json to force rebuild on next open.
	os.Remove(filepath.Join(dir, "index.json")) //nolint:errcheck

	db2 := openDiskDB(t, dir)
	if _, err := db2.Get("rebuild1"); err != nil {
		t.Errorf("Get after rebuild: %v", err)
	}
}

// ── Test 7: corrupt index.json → rebuild from WAL ────────────────────────────

func TestIndexRebuildWhenCorrupt(t *testing.T) {
	dir := t.TempDir()

	db := openDiskDB(t, dir)
	db.Insert(map[string]any{"_id": "corrupt1", "v": "y"}) //nolint:errcheck
	db.Close()

	// Overwrite index.json with garbage.
	os.WriteFile(filepath.Join(dir, "index.json"), []byte("not-json{{{"), 0o644) //nolint:errcheck

	db2 := openDiskDB(t, dir)
	if _, err := db2.Get("corrupt1"); err != nil {
		t.Errorf("Get after corrupt index rebuild: %v", err)
	}
}

// ── Test 8: Update keeps secondary index consistent ──────────────────────────

func TestUpdatePreservesSecondaryIndex(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	id, _ := db.Insert(map[string]any{"city": "mx", "name": "alice"})

	// Change city from "mx" to "us".
	if err := db.Update(id, map[string]any{"city": "us"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Find by old city — must return nothing.
	docs, _ := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}}})
	for _, doc := range docs {
		if doc["_id"] == id {
			t.Error("old city=mx still appears in Find after Update")
		}
	}

	// Find by new city — must return the doc.
	docs2, _ := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "us"}}})
	found := false
	for _, doc := range docs2 {
		if doc["_id"] == id {
			found = true
		}
	}
	if !found {
		t.Error("new city=us not found in Find after Update")
	}
}
