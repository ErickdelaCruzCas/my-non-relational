package tests

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"my-non-relational/api"
	"my-non-relational/engine"
)

// openDB creates a DB backed by a WAL in dir using JSON serialization.
func openDB(t *testing.T, dir string) *api.DB {
	t.Helper()
	db, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", dir, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ── Test: datos sobreviven Close + Open ──────────────────────────────────────

func TestRestartPreservesData(t *testing.T) {
	dir := t.TempDir()

	// ── Sesión 1: escribir datos ──────────────────────────────────────────
	db := openDB(t, dir)

	idAlice, err := db.Insert(map[string]any{"_id": "alice", "name": "Alice", "age": float64(30)})
	if err != nil {
		t.Fatalf("Insert alice: %v", err)
	}
	idBob, err := db.Insert(map[string]any{"_id": "bob", "name": "Bob"})
	if err != nil {
		t.Fatalf("Insert bob: %v", err)
	}
	if err := db.Update(idAlice, map[string]any{"age": float64(31), "city": "mx"}); err != nil {
		t.Fatalf("Update alice: %v", err)
	}
	if err := db.Delete(idBob); err != nil {
		t.Fatalf("Delete bob: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close session 1: %v", err)
	}

	// ── Sesión 2: reabrir y verificar ────────────────────────────────────
	db2, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer db2.Close()

	// Alice debe estar con los campos actualizados.
	alice, err := db2.Get(idAlice)
	if err != nil {
		t.Fatalf("Get alice after restart: %v", err)
	}
	if alice["name"] != "Alice" {
		t.Errorf("name: want Alice, got %v", alice["name"])
	}
	if alice["age"] != float64(31) {
		t.Errorf("age: want 31, got %v", alice["age"])
	}
	if alice["city"] != "mx" {
		t.Errorf("city: want mx, got %v", alice["city"])
	}

	// Bob fue eliminado — debe retornar not found.
	_, err = db2.Get(idBob)
	if err == nil {
		t.Fatal("expected not found for deleted doc bob, got nil error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got %q", err.Error())
	}
}

// ── Test: tail corrupto (escritura parcial) no causa panic ───────────────────

func TestCorruptedTailIgnored(t *testing.T) {
	dir := t.TempDir()

	// Escribir dos documentos válidos.
	db := openDB(t, dir)
	if _, err := db.Insert(map[string]any{"_id": "doc1", "v": "one"}); err != nil {
		t.Fatalf("Insert doc1: %v", err)
	}
	if _, err := db.Insert(map[string]any{"_id": "doc2", "v": "two"}); err != nil {
		t.Fatalf("Insert doc2: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Truncar el archivo a la mitad del header del tercer registro (simulamos
	// que el proceso murió mientras escribía un tercer registro).
	walPath := filepath.Join(dir, "data.log")
	fi, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Añadir 6 bytes basura (menos de la mitad de un header de 13 bytes).
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Write(make([]byte, 6)) //nolint:errcheck
	f.Close()

	newFi, _ := os.Stat(walPath)
	if newFi.Size() != fi.Size()+6 {
		t.Fatalf("expected file to grow by 6 bytes, got %d→%d", fi.Size(), newFi.Size())
	}

	// Reabrir: los dos primeros docs deben estar presentes, sin panic.
	db2, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Reopen after corrupt tail: %v", err)
	}
	defer db2.Close()

	if _, err := db2.Get("doc1"); err != nil {
		t.Errorf("doc1 should survive corrupt tail: %v", err)
	}
	if _, err := db2.Get("doc2"); err != nil {
		t.Errorf("doc2 should survive corrupt tail: %v", err)
	}
}

// ── Test: registro con CRC inválido es descartado ────────────────────────────

func TestInvalidCRCDiscarded(t *testing.T) {
	dir := t.TempDir()

	// Escribir un documento válido.
	db := openDB(t, dir)
	if _, err := db.Insert(map[string]any{"_id": "good", "v": "ok"}); err != nil {
		t.Fatalf("Insert good: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Construir y añadir manualmente un registro con CRC incorrecto.
	// Layout WAL v2: [size uint32LE][type uint32LE][bad_crc uint32LE][version byte][payload]
	payload := []byte(`{"_id":"bad","v":"corrupt"}`)
	var hdr [13]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], 1)           // RecordInsert
	binary.LittleEndian.PutUint32(hdr[8:12], 0xDEADBEEF) // crc incorrecto
	hdr[12] = 1                                          // FormatJSON

	walPath := filepath.Join(dir, "data.log")
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Write(hdr[:])  //nolint:errcheck
	f.Write(payload) //nolint:errcheck
	f.Close()

	// Reabrir: "good" debe estar; "bad" debe ser descartado por CRC inválido.
	db2, err := api.Open(dir, engine.JSONSerializer{})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer db2.Close()

	if _, err := db2.Get("good"); err != nil {
		t.Errorf("good doc should be present: %v", err)
	}
	if _, err := db2.Get("bad"); err == nil {
		t.Error("bad doc (invalid CRC) should have been discarded, but Get succeeded")
	}
}
