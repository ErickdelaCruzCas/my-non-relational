package tests

// Phase 6 — Concurrencia formal
//
// Three tests:
//  1. TestConcurrentReadWrite  — 100 readers + 10 writers for 200ms, no panic, no race.
//  2. TestUniqueIDsUnderConcurrency — 500 concurrent inserts, all IDs distinct.
//  3. TestAtomicCounters — counters reflect exactly the operations executed.
//
// Run with the race detector:
//
//	go test ./tests/ -run TestPhase6 -race -count=3

import (
	"sync"
	"testing"
	"time"

	"my-non-relational/engine"
)

// TestConcurrentReadWrite launches 100 reader goroutines and 10 writer goroutines
// simultaneously against the same DB for 200ms. The test passes if there are
// no panics and the race detector reports no data races.
func TestConcurrentReadWrite(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	// Pre-seed some documents so readers have something to fetch.
	ids := make([]string, 20)
	for i := 0; i < 20; i++ {
		id, err := db.Insert(map[string]any{"score": float64(i)})
		if err != nil {
			t.Fatalf("seed insert %d: %v", i, err)
		}
		ids[i] = id
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// 100 reader goroutines: alternate Get and Find in a tight loop.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					db.Get(ids[0])                //nolint:errcheck
					db.Find(engine.FindRequest{}) //nolint:errcheck
				}
			}
		}()
	}

	// 10 writer goroutines: insert new docs in a tight loop.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					db.Insert(map[string]any{"writer": n}) //nolint:errcheck
				}
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}

// TestUniqueIDsUnderConcurrency inserts 500 documents from 500 concurrent
// goroutines and verifies that every assigned _id is distinct.
// If the ID generator (atomic counter + timestamp) had a race, duplicates appear.
func TestUniqueIDsUnderConcurrency(t *testing.T) {
	const N = 500
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	ids := make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := db.Insert(map[string]any{"n": idx})
			if err != nil {
				t.Errorf("insert[%d]: %v", idx, err)
				return
			}
			ids[idx] = id
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, N)
	for _, id := range ids {
		if id == "" {
			continue // insert error already reported
		}
		if seen[id] {
			t.Errorf("duplicate id: %s", id)
		}
		seen[id] = true
	}
}

// TestAtomicCounters verifies that Stats() reflects the exact number of
// reads, writes, and deletes performed through the public API.
func TestAtomicCounters(t *testing.T) {
	dir := t.TempDir()
	db := openDiskDB(t, dir)

	id1, _ := db.Insert(map[string]any{"a": 1}) // writes: 1
	id2, _ := db.Insert(map[string]any{"b": 2}) // writes: 2
	db.Get(id1)                                 //nolint:errcheck  reads: 1
	db.Get(id2)                                 //nolint:errcheck  reads: 2
	db.Find(engine.FindRequest{})               //nolint:errcheck  reads: 3
	db.Update(id1, map[string]any{"a": 9})      //nolint:errcheck  writes: 3
	db.Delete(id2)                              //nolint:errcheck  deletes: 1

	snap := db.Stats()
	if snap.WritesTotal != 3 {
		t.Errorf("WritesTotal: want 3, got %d", snap.WritesTotal)
	}
	if snap.ReadsTotal != 3 {
		t.Errorf("ReadsTotal: want 3, got %d", snap.ReadsTotal)
	}
	if snap.DeletesTotal != 1 {
		t.Errorf("DeletesTotal: want 1, got %d", snap.DeletesTotal)
	}
}
