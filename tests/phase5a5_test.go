package tests

// Phase 5a.5 — AVL Tree como índice de rangos
//
// Tests verify two things:
//  1. Correctness: all range ops (gt/gte/lt/lte/between) still work correctly
//     now that the live DB uses RangeAVLIndex instead of RangeIndex.
//  2. Balance: TestAVLBalanceVsBST is the educational core — inserts 200 keys
//     in sorted order and compares tree heights. BST degenerates to height ≈ 200;
//     AVL stays balanced at height ≤ 15 (1.44·log₂(200) ≈ 10.8).

import (
	"testing"

	"my-non-relational/engine"
)

// ── Correctness tests (same cases as Phase 5a, now via AVL) ──────────────────

func TestAVLRangeGt(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "score": 50.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "c", "score": 70.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "d", "score": 50.0}) //nolint:errcheck // boundary — excluded

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0]["_id"] != "c" {
		t.Errorf("expected _id=c, got %v", docs[0]["_id"])
	}
}

func TestAVLRangeGte(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 30.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "score": 50.0}) //nolint:errcheck // boundary — included
	db.Insert(map[string]any{"_id": "c", "score": 70.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "score", Op: "gte", Value: 50.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	for _, doc := range docs {
		if toFloat(doc["score"]) < 50.0 {
			t.Errorf("gte violated: score=%v", doc["score"])
		}
	}
}

func TestAVLRangeLt(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "age": 20.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "age": 30.0}) //nolint:errcheck // boundary — excluded
	db.Insert(map[string]any{"_id": "c", "age": 40.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "age", Op: "lt", Value: 30.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0]["_id"] != "a" {
		t.Errorf("expected _id=a, got %v", docs[0]["_id"])
	}
}

func TestAVLRangeLte(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "age": 20.0}) //nolint:errcheck
	db.Insert(map[string]any{"_id": "b", "age": 30.0}) //nolint:errcheck // boundary — included
	db.Insert(map[string]any{"_id": "c", "age": 40.0}) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{
		Filters: []engine.Filter{{Field: "age", Op: "lte", Value: 30.0}},
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	for _, doc := range docs {
		if toFloat(doc["age"]) > 30.0 {
			t.Errorf("lte violated: age=%v", doc["age"])
		}
	}
}

func TestAVLBetween(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	db.Insert(map[string]any{"_id": "a", "score": 10.0}) //nolint:errcheck // below
	db.Insert(map[string]any{"_id": "b", "score": 30.0}) //nolint:errcheck // lo — included
	db.Insert(map[string]any{"_id": "c", "score": 50.0}) //nolint:errcheck // inside
	db.Insert(map[string]any{"_id": "d", "score": 70.0}) //nolint:errcheck // hi — included
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
			t.Errorf("between violated: score=%v", s)
		}
	}
}

func TestAVLRangeSortLimit(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	for _, s := range []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		db.Insert(map[string]any{"score": s}) //nolint:errcheck
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
			t.Errorf("[%d] want %.0f got %.0f", i, want[i], got)
		}
	}
}

func TestAVLAfterUpdate(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	id, _ := db.Insert(map[string]any{"score": 30.0})
	db.Update(id, map[string]any{"score": 80.0}) //nolint:errcheck

	above, _ := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}}})
	found := false
	for _, doc := range above {
		if doc["_id"] == id {
			found = true
		}
	}
	if !found {
		t.Error("updated doc not found in score>50")
	}

	below, _ := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "score", Op: "lt", Value: 50.0}}})
	for _, doc := range below {
		if doc["_id"] == id {
			t.Error("stale score=30 still in score<50 after update to 80")
		}
	}
}

func TestAVLAfterDelete(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	keep, _ := db.Insert(map[string]any{"score": 80.0})
	gone, _ := db.Insert(map[string]any{"score": 90.0})
	db.Delete(gone) //nolint:errcheck

	docs, err := db.Find(engine.FindRequest{Filters: []engine.Filter{{Field: "score", Op: "gt", Value: 50.0}}})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, doc := range docs {
		if doc["_id"] == gone {
			t.Error("deleted doc still in range query")
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

// ── Educational contrast test ─────────────────────────────────────────────────

// TestAVLBalanceVsBST is the core of Phase 5a.5.
//
// It inserts N keys in strictly ascending order — the worst case for an
// unbalanced BST — into both a RangeIndex (BST) and a RangeAVLIndex (AVL).
// Then it compares tree heights and verifies that both return the same results.
//
// Expected output (visible with -v):
//
//	BST height = 200  (degenerate linked list, O(N))
//	AVL height = 11   (balanced, O(log N) — 1.44·log₂(200) ≈ 10.8)
func TestAVLBalanceVsBST(t *testing.T) {
	const N = 200

	bstIdx := engine.NewRangeIndex()    // naive BST
	avlIdx := engine.NewRangeAVLIndex() // self-balancing AVL

	for i := 0; i < N; i++ {
		doc := map[string]any{"score": float64(i)}
		bstIdx.AddDoc(doc, int64(i))
		avlIdx.AddDoc(doc, int64(i))
	}

	bstH := bstIdx.MaxTreeHeight("score")
	avlH := avlIdx.MaxTreeHeight("score")

	t.Logf("N=%d sorted inserts", N)
	t.Logf("BST height = %d  (expected ≈ %d, O(N) degenerate linked list)", bstH, N)
	t.Logf("AVL height = %d  (expected ≤ 15,  O(log N) balanced — 1.44·log₂(%d) ≈ %.1f)", avlH, N, 1.44*logBase2(N))

	// BST must degenerate on sorted input.
	if bstH < N/2 {
		t.Errorf("BST should be degenerate (height >= %d), got %d", N/2, bstH)
	}

	// AVL must stay balanced: height ≤ 1.44·log₂(N) + slack.
	const maxAVLHeight = 15
	if avlH > maxAVLHeight {
		t.Errorf("AVL too tall: height %d > %d", avlH, maxAVLHeight)
	}

	// Both must return the same correct results for a range query.
	bstRes := bstIdx.Query("score", "between", [2]float64{50, 100})
	avlRes := avlIdx.Query("score", "between", [2]float64{50, 100})
	if len(bstRes) != len(avlRes) {
		t.Errorf("result count mismatch: BST=%d AVL=%d", len(bstRes), len(avlRes))
	}
	// Expecting keys 50..100 inclusive = 51 docs.
	if len(avlRes) != 51 {
		t.Errorf("expected 51 docs in [50,100], got %d", len(avlRes))
	}
}

// logBase2 returns log₂(n) as float64. Used only for the diagnostic log line.
func logBase2(n int) float64 {
	if n <= 0 {
		return 0
	}
	// Compute using repeated halving (no stdlib math.Log).
	x := float64(n)
	result := 0.0
	for x >= 2 {
		x /= 2
		result++
	}
	return result
}
