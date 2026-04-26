package tests

// Phase 4 — Motor de consultas + Min-Heap TopK
//
// Tests cover:
//   - FindRequest with eq filter (disk mode uses secondary index)
//   - No filter → all docs returned
//   - Sort ascending / descending
//   - TopK: sort + limit → min-heap O(N log K)
//   - TopK when limit > total docs (returns all)
//   - Projection: only listed fields kept; _id always present
//   - Deleted docs absent from Find
//   - Updated field value reflected in Find

import (
	"testing"

	"my-non-relational/engine"
)

// ── Test 1: single eq filter (secondary index strategy) ──────────────────────

func TestFindEquality(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "city": "mx", "score": 10.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "city": "us", "score": 20.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "c", "city": "mx", "score": 30.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs with city=mx, got %d", len(docs))
	}
	for _, doc := range docs {
		if doc["city"] != "mx" {
			t.Errorf("unexpected city=%v", doc["city"])
		}
	}
}

// ── Test 2: no filter → all docs ─────────────────────────────────────────────

func TestFindNoFilterDisk(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	for _, id := range []string{"x1", "x2", "x3", "x4"} {
		db.Insert(map[string]any{"_id": id, "v": 1}) //nolint:errcheck
	}

	docs, err := db.Find(engine.FindRequest{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 4 {
		t.Errorf("expected 4 docs, got %d", len(docs))
	}
}

// ── Test 3: sort ascending ────────────────────────────────────────────────────

func TestFindSortAsc(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "p3", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "p1", "score": 10.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "p2", "score": 20.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{SortBy: "score", SortAsc: true})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}
	for i := 1; i < len(docs); i++ {
		prev := toFloat(docs[i-1]["score"])
		curr := toFloat(docs[i]["score"])
		if prev > curr {
			t.Errorf("asc order violated at [%d]: %v > %v", i, prev, curr)
		}
	}
}

// ── Test 4: sort descending ───────────────────────────────────────────────────

func TestFindSortDesc(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "q1", "score": 10.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "q3", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "q2", "score": 20.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{SortBy: "score", SortAsc: false})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}
	for i := 1; i < len(docs); i++ {
		prev := toFloat(docs[i-1]["score"])
		curr := toFloat(docs[i]["score"])
		if prev < curr {
			t.Errorf("desc order violated at [%d]: %v < %v", i, prev, curr)
		}
	}
}

// ── Test 5: TopK — sort + limit uses min-heap ─────────────────────────────────

func TestTopKHeap(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	scores := []float64{5, 3, 8, 1, 9, 2, 7, 4, 6, 10}
	for i, s := range scores {
		db.Insert(map[string]any{"score": s, "seq": float64(i)}) //nolint:errcheck
	}

	const K = 3
	// Top 3 descending → scores 10, 9, 8
	docs, err := db.Find(engine.FindRequest{SortBy: "score", SortAsc: false, Limit: K})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != K {
		t.Fatalf("expected %d docs, got %d", K, len(docs))
	}

	want := []float64{10, 9, 8}
	for i, doc := range docs {
		got := toFloat(doc["score"])
		if got != want[i] {
			t.Errorf("TopK[%d]: want score=%.0f got %.0f", i, want[i], got)
		}
	}

	// Verify against a full sort (ground truth).
	all, _ := db.Find(engine.FindRequest{SortBy: "score", SortAsc: false})
	for i := range docs {
		heapScore := toFloat(docs[i]["score"])
		fullScore := toFloat(all[i]["score"])
		if heapScore != fullScore {
			t.Errorf("heap[%d]=%.0f but full_sort[%d]=%.0f", i, heapScore, i, fullScore)
		}
	}
}

// ── Test 6: TopK when limit > total docs → return all ────────────────────────

func TestTopKSmall(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "s1", "score": 1.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "s2", "score": 2.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{SortBy: "score", SortAsc: false, Limit: 100})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}
}

// ── Test 7: projection ────────────────────────────────────────────────────────

func TestProjection(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "proj1", "name": "alice", "age": 30.0, "city": "mx"}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Projection: []string{"name"},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	doc := docs[0]

	// _id always present.
	if _, ok := doc["_id"]; !ok {
		t.Error("projection must always include _id")
	}
	// "name" requested — must be present.
	if _, ok := doc["name"]; !ok {
		t.Error("projected field 'name' missing")
	}
	// "age" and "city" not requested — must be absent.
	if _, ok := doc["age"]; ok {
		t.Error("non-projected field 'age' should be absent")
	}
	if _, ok := doc["city"]; ok {
		t.Error("non-projected field 'city' should be absent")
	}
}

// ── Test 8: deleted docs not returned by Find ─────────────────────────────────

func TestDeletedDocsNotFound(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "keep", "city": "mx"}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "gone", "city": "mx"}) //nolint:errcheck
	db.Delete("gone")                                      //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, doc := range docs {
		if doc["_id"] == "gone" {
			t.Error("deleted doc 'gone' still returned by Find")
		}
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(docs))
	}
}

// ── Test 9: updated field reflected in Find ───────────────────────────────────

func TestFindAfterUpdate(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	id, _ := db.Insert(map[string]any{"city": "mx", "score": 5.0})

	if err := db.Update(id, map[string]any{"city": "eu", "score": 99.0}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Old filter must find nothing.
	old, _ := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "mx"}},
	})
	for _, doc := range old {
		if doc["_id"] == id {
			t.Error("stale city=mx still returned after Update")
		}
	}

	// New filter must find the doc.
	updated, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "city", Op: "eq", Value: "eu"}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	found := false
	for _, doc := range updated {
		if doc["_id"] == id {
			if toFloat(doc["score"]) != 99.0 {
				t.Errorf("expected score=99, got %v", doc["score"])
			}
			found = true
		}
	}
	if !found {
		t.Error("updated doc not found by new city=eu filter")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// toFloat is a local alias matching engine.toFloat's coercion logic.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
