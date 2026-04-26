package tests

// Phase 5a — BST Naïve + Consultas de Rango
//
// Tests verify that the range query pipeline works correctly end-to-end:
//   - gt / gte / lt / lte → BST range scan (strategy=range_bst)
//   - between → BST range scan with [lo, hi] bounds
//   - Range filter + sort + limit → BST + Min-Heap combined
//   - Index stays consistent after Update and Delete
//   - Empty results (no matching docs)
//   - Full collection match (>= 0)

import (
	"testing"

	"my-non-relational/engine"
)

// ── Test 1: gt ────────────────────────────────────────────────────────────────

func TestRangeGt(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "score": 50.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "c", "score": 70.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "d", "score": 50.0}) //nolint:errcheck // boundary, must be excluded

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc with score>50, got %d", len(docs))
	}
	if docs[0]["_id"] != "c" {
		t.Errorf("expected _id=c, got %v", docs[0]["_id"])
	}
}

// ── Test 2: gte (boundary inclusive) ─────────────────────────────────────────

func TestRangeGte(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "score": 50.0}) //nolint:errcheck // boundary, must be included
	db.Insert(map[string]any{"_id": "c", "score": 70.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gte", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs with score>=50, got %d", len(docs))
	}
	for _, doc := range docs {
		if toFloat(doc["score"]) < 50.0 {
			t.Errorf("gte filter violated: score=%v", doc["score"])
		}
	}
}

// ── Test 3: lt ────────────────────────────────────────────────────────────────

func TestRangeLt(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "age": 20.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "age": 30.0}) //nolint:errcheck // boundary, must be excluded
	db.Insert(map[string]any{"_id": "c", "age": 40.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "age", Op: "lt", Value: 30.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc with age<30, got %d", len(docs))
	}
	if docs[0]["_id"] != "a" {
		t.Errorf("expected _id=a, got %v", docs[0]["_id"])
	}
}

// ── Test 4: lte (boundary inclusive) ─────────────────────────────────────────

func TestRangeLte(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "age": 20.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "age": 30.0}) //nolint:errcheck // boundary, must be included
	db.Insert(map[string]any{"_id": "c", "age": 40.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "age", Op: "lte", Value: 30.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs with age<=30, got %d", len(docs))
	}
	for _, doc := range docs {
		if toFloat(doc["age"]) > 30.0 {
			t.Errorf("lte filter violated: age=%v", doc["age"])
		}
	}
}

// ── Test 5: between [lo, hi] inclusive ───────────────────────────────────────

func TestBetween(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 10.0}) //nolint:errcheck // below
	db.Insert(map[string]any{"_id": "b", "score": 30.0}) //nolint:errcheck // lo boundary — included
	db.Insert(map[string]any{"_id": "c", "score": 50.0}) //nolint:errcheck // inside
	db.Insert(map[string]any{"_id": "d", "score": 70.0}) //nolint:errcheck // hi boundary — included
	db.Insert(map[string]any{"_id": "e", "score": 90.0}) //nolint:errcheck // above

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "between", Value: [2]float64{30, 70}}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs in [30,70], got %d", len(docs))
	}
	for _, doc := range docs {
		s := toFloat(doc["score"])
		if s < 30 || s > 70 {
			t.Errorf("between filter violated: score=%v", s)
		}
	}
}

// ── Test 6: range + sort + limit (BST + heap) ────────────────────────────────

func TestRangeSortLimit(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	scores := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	for i, s := range scores {
		db.Insert(map[string]any{"score": s, "seq": float64(i)}) //nolint:errcheck
	}

	// score > 40, descending, top 3 → 100, 90, 80
	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 40.0}},
		SortBy:  "score",
		SortAsc: false,
		Limit:   3,
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}
	want := []float64{100, 90, 80}
	for i, doc := range docs {
		got := toFloat(doc["score"])
		if got != want[i] {
			t.Errorf("[%d] want score=%.0f got %.0f", i, want[i], got)
		}
	}
}

// ── Test 7: range after update (doc crosses boundary) ────────────────────────

func TestRangeAfterUpdate(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	id, _ := db.Insert(map[string]any{"score": 30.0})

	// Update: score 30 → 80 (crosses the >50 boundary).
	if err := db.Update(id, map[string]any{"score": 80.0}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Old range (>50): must include the doc now.
	above, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find above: %v", err)
	}
	found := false
	for _, doc := range above {
		if doc["_id"] == id {
			found = true
		}
	}
	if !found {
		t.Error("updated doc not found in score>50 after update to 80")
	}

	// Old range (<50): must no longer include the doc.
	below, _ := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "lt", Value: 50.0}},
	})
	for _, doc := range below {
		if doc["_id"] == id {
			t.Error("stale score=30 still appears in score<50 after update to 80")
		}
	}
}

// ── Test 8: range after delete ────────────────────────────────────────────────

func TestRangeAfterDelete(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	keep, _ := db.Insert(map[string]any{"score": 80.0})
	gone, _ := db.Insert(map[string]any{"score": 90.0})

	db.Delete(gone) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, doc := range docs {
		if doc["_id"] == gone {
			t.Error("deleted doc still returned by range query")
		}
	}
	foundKeep := false
	for _, doc := range docs {
		if doc["_id"] == keep {
			foundKeep = true
		}
	}
	if !foundKeep {
		t.Error("non-deleted doc missing from range query")
	}
}

// ── Test 9: empty result ──────────────────────────────────────────────────────

func TestRangeEmptyResult(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 10.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "score": 20.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 99.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}
}

// ── Test 10: full collection via range (>= 0) ─────────────────────────────────

func TestRangeAllDocs(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	const N = 20
	for i := 0; i < N; i++ {
		db.Insert(map[string]any{"score": float64(i * 5)}) //nolint:errcheck
	}

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gte", Value: 0.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != N {
		t.Errorf("expected %d docs, got %d", N, len(docs))
	}
	for _, doc := range docs {
		if toFloat(doc["score"]) < 0 {
			t.Errorf("gte 0 returned negative score: %v", doc["score"])
		}
	}
}
