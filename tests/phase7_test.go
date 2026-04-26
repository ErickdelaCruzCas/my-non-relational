package tests

// Phase 7 — JSON → MsgPack migration
//
// Three tests + two benchmarks:
//  1. TestMsgPackRestartSurvival — inserts with MsgPack, restarts, verifies docs survive.
//  2. TestMixedWAL              — JSON session then MsgPack session; both docs accessible.
//  3. BenchmarkJSON / BenchmarkMsgPack — compare throughput and WAL size.
//
// Run:
//
//	go test ./tests/ -run TestPhase7 -v
//	go test ./tests/ -bench=. -benchmem -run=^$

import (
	"testing"

	"my-non-relational/api"
	"my-non-relational/engine"
)

// TestMsgPackRestartSurvival verifies that documents written with MsgPack
// survive a DB close + reopen cycle.
func TestMsgPackRestartSurvival(t *testing.T) {
	dir := t.TempDir()

	// ── Session 1: insert with MsgPack ────────────────────────────────────────
	db1, err := api.Open(dir, engine.MsgPackSerializer{})
	if err != nil {
		t.Fatalf("Open session 1: %v", err)
	}
	id, err := db1.Insert(map[string]any{"name": "alice", "score": float64(99)})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close session 1: %v", err)
	}

	// ── Session 2: reopen with MsgPack, verify doc ────────────────────────────
	db2, err := api.Open(dir, engine.MsgPackSerializer{})
	if err != nil {
		t.Fatalf("Open session 2: %v", err)
	}
	defer db2.Close()

	doc, err := db2.Get(id)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if doc["name"] != "alice" {
		t.Errorf("name: want alice, got %v", doc["name"])
	}
	if doc["score"] != float64(99) {
		t.Errorf("score: want 99, got %v", doc["score"])
	}
}

// TestMixedWAL verifies that a WAL with both JSON and MsgPack records can be
// replayed correctly. This simulates an interrupted migration.
//
// Timeline:
//
//	Session 1 (JSON) → inserts id1
//	Session 2 (MsgPack) → inserts id2 (same WAL file, different version bytes)
//	Session 3 (MsgPack) → both id1 and id2 must be accessible
func TestMixedWAL(t *testing.T) {
	dir := t.TempDir()

	// ── Session 1: JSON ───────────────────────────────────────────────────────
	db1, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Open session 1 (JSON): %v", err)
	}
	id1, err := db1.Insert(map[string]any{"src": "json", "v": float64(1)})
	if err != nil {
		t.Fatalf("Insert JSON doc: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close session 1: %v", err)
	}

	// ── Session 2: MsgPack (appends to same WAL) ──────────────────────────────
	db2, err := api.Open(dir, engine.MsgPackSerializer{})
	if err != nil {
		t.Fatalf("Open session 2 (MsgPack): %v", err)
	}
	id2, err := db2.Insert(map[string]any{"src": "msgpack", "v": float64(2)})
	if err != nil {
		t.Fatalf("Insert MsgPack doc: %v", err)
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("Close session 2: %v", err)
	}

	// ── Session 3: reopen with MsgPack — both docs must be accessible ─────────
	db3, err := api.Open(dir, engine.MsgPackSerializer{})
	if err != nil {
		t.Fatalf("Open session 3: %v", err)
	}
	defer db3.Close()

	doc1, err := db3.Get(id1)
	if err != nil {
		t.Fatalf("Get JSON doc (id1) after mixed WAL: %v", err)
	}
	if doc1["src"] != "json" {
		t.Errorf("id1.src: want json, got %v", doc1["src"])
	}

	doc2, err := db3.Get(id2)
	if err != nil {
		t.Fatalf("Get MsgPack doc (id2) after mixed WAL: %v", err)
	}
	if doc2["src"] != "msgpack" {
		t.Errorf("id2.src: want msgpack, got %v", doc2["src"])
	}

	// Both docs must appear in Find
	all, err := db3.Find(engine.FindRequest{})
	if err != nil {
		t.Fatalf("Find all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("Find: want 2 docs, got %d", len(all))
	}
}

// BenchmarkJSON measures insert throughput with JSON serialization.
func BenchmarkJSON(b *testing.B) {
	dir := b.TempDir()
	db, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Insert(map[string]any{"n": i, "name": "benchmark", "score": float64(i) * 1.5}) //nolint:errcheck
	}
}

// BenchmarkMsgPack measures insert throughput with MsgPack serialization.
func BenchmarkMsgPack(b *testing.B) {
	dir := b.TempDir()
	db, err := api.Open(dir, engine.MsgPackSerializer{})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Insert(map[string]any{"n": i, "name": "benchmark", "score": float64(i) * 1.5}) //nolint:errcheck
	}
}
