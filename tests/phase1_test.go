package tests

import (
	"strings"
	"testing"

	"my-non-relational/api"
	"my-non-relational/engine"
)

// ─── Helpers ────────────────────────────────────────────────────────────────

func setupDB(t *testing.T) *api.DB {
	t.Helper()
	db, err := api.Open("")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ─── API tests (black-box) ───────────────────────────────────────────────────

func TestInsertGeneratesID(t *testing.T) {
	db := setupDB(t)
	id, err := db.Insert(map[string]any{"name": "alice"})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	// Generated IDs have the form "<nanos>-<counter>"
	if !strings.Contains(id, "-") {
		t.Errorf("expected id to contain '-', got %q", id)
	}
}

func TestInsertWithExplicitID(t *testing.T) {
	db := setupDB(t)
	id, err := db.Insert(map[string]any{"_id": "custom-id", "name": "bob"})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if id != "custom-id" {
		t.Errorf("expected id %q, got %q", "custom-id", id)
	}
}

func TestInsertDuplicateID(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "dup"}) //nolint:errcheck
	_, err := db.Insert(map[string]any{"_id": "dup"})
	if err == nil {
		t.Fatal("expected error for duplicate id, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("expected 'duplicate id' in error, got %q", err.Error())
	}
}

func TestGetExisting(t *testing.T) {
	db := setupDB(t)
	id, _ := db.Insert(map[string]any{"_id": "g1", "name": "carol", "score": float64(99)})

	doc, err := db.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if doc["name"] != "carol" {
		t.Errorf("expected name %q, got %v", "carol", doc["name"])
	}
	if doc["score"] != float64(99) {
		t.Errorf("expected score 99, got %v", doc["score"])
	}
	if doc["_id"] != "g1" {
		t.Errorf("expected _id %q, got %v", "g1", doc["_id"])
	}
}

func TestGetNotFound(t *testing.T) {
	db := setupDB(t)
	_, err := db.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestGetDefensiveCopy(t *testing.T) {
	db := setupDB(t)
	id, _ := db.Insert(map[string]any{"_id": "dc1", "x": "original"})

	doc, _ := db.Get(id)
	doc["x"] = "mutated" // mutate the returned copy

	// The stored document must be unchanged.
	doc2, _ := db.Get(id)
	if doc2["x"] != "original" {
		t.Errorf("defensive copy broken: stored value changed to %v", doc2["x"])
	}
}

func TestUpdateExisting(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "u1", "name": "alice", "age": float64(30)}) //nolint:errcheck

	if err := db.Update("u1", map[string]any{"age": float64(31), "city": "mx"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	doc, _ := db.Get("u1")
	if doc["name"] != "alice" {
		t.Errorf("existing field 'name' should be preserved, got %v", doc["name"])
	}
	if doc["age"] != float64(31) {
		t.Errorf("field 'age' should be updated to 31, got %v", doc["age"])
	}
	if doc["city"] != "mx" {
		t.Errorf("new field 'city' should be 'mx', got %v", doc["city"])
	}
	if doc["_id"] != "u1" {
		t.Errorf("_id should be preserved as 'u1', got %v", doc["_id"])
	}
}

func TestUpdateNotFound(t *testing.T) {
	db := setupDB(t)
	err := db.Update("ghost", map[string]any{"x": 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestUpdateCannotChangeID(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "u2"}) //nolint:errcheck

	err := db.Update("u2", map[string]any{"_id": "u2-new"})
	if err == nil {
		t.Fatal("expected error when changing _id, got nil")
	}
	if !strings.Contains(err.Error(), "cannot change _id") {
		t.Errorf("expected 'cannot change _id' in error, got %q", err.Error())
	}
}

func TestUpdatePreservesIDWhenSame(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "u3", "v": "a"}) //nolint:errcheck

	// Passing the same _id in partial is allowed.
	if err := db.Update("u3", map[string]any{"_id": "u3", "v": "b"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	doc, _ := db.Get("u3")
	if doc["v"] != "b" {
		t.Errorf("expected v=b, got %v", doc["v"])
	}
	if doc["_id"] != "u3" {
		t.Errorf("_id should still be 'u3', got %v", doc["_id"])
	}
}

func TestDeleteExisting(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "d1"}) //nolint:errcheck

	if err := db.Delete("d1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	db := setupDB(t)
	err := db.Delete("ghost")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestGetAfterDelete(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "d2"}) //nolint:errcheck
	db.Delete("d2")                        //nolint:errcheck

	_, err := db.Get("d2")
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

// ─── HashMap tests (white-box) ───────────────────────────────────────────────

func TestHashMapBasicSetGet(t *testing.T) {
	m := engine.NewHashMap()
	m.Set("k1", map[string]any{"v": 1})

	val, ok := m.Get("k1")
	if !ok {
		t.Fatal("expected to find k1")
	}
	if val["v"] != 1 {
		t.Errorf("expected v=1, got %v", val["v"])
	}
	if m.Count() != 1 {
		t.Errorf("expected Count=1, got %d", m.Count())
	}
}

func TestHashMapUpdateExisting(t *testing.T) {
	m := engine.NewHashMap()
	m.Set("k1", map[string]any{"v": 1})
	m.Set("k1", map[string]any{"v": 2}) // update

	val, _ := m.Get("k1")
	if val["v"] != 2 {
		t.Errorf("expected updated v=2, got %v", val["v"])
	}
	if m.Count() != 1 {
		t.Errorf("updating existing key must not change Count; got %d", m.Count())
	}
}

func TestHashMapDelete(t *testing.T) {
	m := engine.NewHashMap()
	m.Set("k1", map[string]any{"v": 1})
	m.Set("k2", map[string]any{"v": 2})

	deleted := m.Delete("k1")
	if !deleted {
		t.Fatal("expected Delete to return true for existing key")
	}
	if m.Count() != 1 {
		t.Errorf("expected Count=1 after delete, got %d", m.Count())
	}
	_, ok := m.Get("k1")
	if ok {
		t.Error("deleted key k1 should not be found")
	}
	_, ok = m.Get("k2")
	if !ok {
		t.Error("non-deleted key k2 should still be found")
	}
}

func TestHashMapDeleteNonExistent(t *testing.T) {
	m := engine.NewHashMap()
	deleted := m.Delete("ghost")
	if deleted {
		t.Error("Delete of non-existent key should return false")
	}
}

// TestHashMapTombstoneProbing verifies that Get still works for keys that lie
// beyond a tombstone in a probe chain.
//
// Strategy: insert 10 keys into a capacity-16 table (load ≈ 0.625, no rehash).
// With 10 elements in 16 slots, collisions are expected by birthday paradox.
// Delete keys at positions 2, 5, 8 (by insertion order) to create tombstones.
// All remaining 7 keys must still be reachable.
func TestHashMapTombstoneProbing(t *testing.T) {
	m := engine.NewHashMap()
	keys := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
	for i, k := range keys {
		m.Set(k, map[string]any{"i": i})
	}

	toDelete := map[string]bool{"k2": true, "k5": true, "k8": true}
	for k := range toDelete {
		if !m.Delete(k) {
			t.Fatalf("Delete(%q) returned false unexpectedly", k)
		}
	}

	if m.Count() != 7 {
		t.Errorf("expected Count=7, got %d", m.Count())
	}

	for _, k := range keys {
		if toDelete[k] {
			if _, ok := m.Get(k); ok {
				t.Errorf("deleted key %q should not be found", k)
			}
		} else {
			if _, ok := m.Get(k); !ok {
				t.Errorf("key %q should still be reachable after tombstone insertions", k)
			}
		}
	}
}

// ─── Find tests ─────────────────────────────────────────────────────────────

func TestFindNoFilter(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "a", "city": "mx"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "city": "us"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "c", "city": "mx"}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(docs) != 3 {
		t.Errorf("expected 3 docs, got %d", len(docs))
	}
}

func TestFindWithFilter(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "a", "city": "mx"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "city": "us"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "c", "city": "mx"}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}}})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs with city=mx, got %d", len(docs))
	}
	for _, doc := range docs {
		if doc["city"] != "mx" {
			t.Errorf("found doc with city=%v, expected mx", doc["city"])
		}
	}
}

func TestFindExcludesDeleted(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "a", "city": "mx"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "city": "mx"}) //nolint:errcheck
	db.Delete("b")                                      //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}}})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc after delete, got %d", len(docs))
	}
	if docs[0]["_id"] != "a" {
		t.Errorf("expected _id=a, got %v", docs[0]["_id"])
	}
}

func TestFindDefensiveCopy(t *testing.T) {
	db := setupDB(t)
	db.Insert(map[string]any{"_id": "a", "v": "original"}) //nolint:errcheck

	docs, _ := db.Find(engine.FindRequest{})
	docs[0]["v"] = "mutated"

	docs2, _ := db.Find(engine.FindRequest{})
	if docs2[0]["v"] != "original" {
		t.Errorf("Find must return defensive copies, got %v", docs2[0]["v"])
	}
}

// TestHashMapRehash verifies that inserting beyond the load factor threshold
// triggers a rehash (capacity doubles) and all elements remain accessible.
func TestHashMapRehash(t *testing.T) {
	m := engine.NewHashMap()
	initialCap := m.Capacity() // 16

	// Insert 12 elements: the 12th triggers rehash (12/16 = 0.75 > 0.7).
	inserted := make([]string, 12)
	for i := 0; i < 12; i++ {
		k := strings.Repeat("x", i+1) // "x", "xx", "xxx", ...
		m.Set(k, map[string]any{"n": i})
		inserted[i] = k
	}

	if m.Capacity() <= initialCap {
		t.Errorf("expected capacity to grow beyond %d after rehash, got %d", initialCap, m.Capacity())
	}
	if m.Count() != 12 {
		t.Errorf("expected Count=12, got %d", m.Count())
	}

	// All elements must still be accessible after rehash.
	for i, k := range inserted {
		val, ok := m.Get(k)
		if !ok {
			t.Errorf("key %q lost after rehash", k)
			continue
		}
		if val["n"] != i {
			t.Errorf("key %q: expected n=%d, got %v", k, i, val["n"])
		}
	}
}
