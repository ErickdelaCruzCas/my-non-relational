// cmd/seed — database seeder for debugging Phase 3.
//
// Usage:
//
//	go run ./cmd/seed [flags]
//
// Flags:
//
//	-count  int     number of documents to insert (default 30)
//	-dir    string  data directory (default "data/")
//	-mode   string  "with-index" (keep index.json) | "no-index" (delete it after seeding)
//	-fresh          wipe the data dir before seeding
//
// Two modes explained:
//
//	with-index  → normal cold start next run: Open() hits fast path (loadIndex → spotCheck).
//	no-index    → deletes index.json after seeding: Open() hits slow path (rebuildFromWAL).
//
// This lets you set a breakpoint in api/db.go:Open and step through both startup
// branches in the IDE debugger.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"

	"my-non-relational/api"
	"my-non-relational/engine"
)

// ── Sample data pools ─────────────────────────────────────────────────────────

var (
	names      = []string{"alice", "bob", "carol", "dave", "eve", "frank", "grace", "hiro", "ivan", "julia"}
	cities     = []string{"mx", "nyc", "london", "tokyo", "paris", "berlin", "bogota", "lima", "cairo", "sydney"}
	countries  = []string{"mx", "us", "uk", "jp", "fr", "de", "co", "pe", "eg", "au"}
	categories = []string{"admin", "user", "guest", "editor", "viewer"}
)

func pick(pool []string) string { return pool[rand.Intn(len(pool))] }

// makeDoc builds a realistic document for position i.
func makeDoc(i int) map[string]any {
	cityIdx := rand.Intn(len(cities))
	return map[string]any{
		"name":     pick(names),
		"age":      20 + rand.Intn(45),
		"city":     cities[cityIdx],
		"country":  countries[cityIdx], // correlated with city
		"category": pick(categories),
		"active":   rand.Intn(2) == 1,
		"score":    rand.Intn(100),
		"seq":      i, // unique sequential field, useful for range queries later
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	count := flag.Int("count", 30, "number of documents to insert")
	dir := flag.String("dir", "data/", "data directory")
	mode := flag.String("mode", "with-index", `"with-index" | "no-index"`)
	fresh := flag.Bool("fresh", false, "wipe data dir before seeding")
	flag.Parse()

	if *mode != "with-index" && *mode != "no-index" {
		log.Fatalf("unknown mode %q — use 'with-index' or 'no-index'", *mode)
	}

	// ── Fresh wipe ────────────────────────────────────────────────────────────
	if *fresh {
		if err := os.RemoveAll(*dir); err != nil {
			log.Fatalf("remove %s: %v", *dir, err)
		}
		fmt.Printf("[seed] wiped %s\n", *dir)
	}

	// ── Open DB and insert documents ──────────────────────────────────────────
	engine.LogInfo("[seed] open", "dir", *dir, "mode", *mode, "count", *count)

	db, err := api.Open(*dir, engine.JSONSerializer{})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	inserted := 0
	for i := 0; i < *count; i++ {
		doc := makeDoc(i)
		id, err := db.Insert(doc)
		if err != nil {
			log.Printf("insert %d: %v", i, err)
			continue
		}
		inserted++
		fmt.Printf("[seed] inserted  id=%-36s  name=%-8s  city=%-8s  seq=%d\n",
			id, doc["name"], doc["city"], doc["seq"])
	}

	// Close saves index.json.
	if err := db.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}

	fmt.Printf("\n[seed] done — %d documents in %s\n", inserted, *dir)

	// ── no-index mode: delete index.json ──────────────────────────────────────
	indexPath := filepath.Join(*dir, "index.json")
	if *mode == "no-index" {
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("remove index.json: %v", err)
		}
		fmt.Println("[seed] deleted index.json — next Open() will rebuild from WAL")
		fmt.Println("       → breakpoint: api/db.go  rebuildFromWAL()")
	} else {
		fmt.Println("[seed] index.json kept — next Open() will use fast path")
		fmt.Println("       → breakpoint: api/db.go  loadIndex() + spotCheck()")
	}

	printSummary(*dir, *mode)
}

// printSummary tells the user exactly where to set breakpoints for each mode.
func printSummary(dir, mode string) {
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────")
	fmt.Printf("  Data dir : %s\n", dir)
	fmt.Printf("  Mode     : %s\n", mode)
	fmt.Println()
	if mode == "with-index" {
		fmt.Println("  Breakpoints — fast-path startup:")
		fmt.Println("    api/db.go          Open()        ~line 80  (loadIndex branch)")
		fmt.Println("    api/db.go          loadIndex()             (index.json parsing)")
		fmt.Println("    api/db.go          spotCheck()             (WAL verification)")
		fmt.Println()
		fmt.Println("  Then trace a Get():")
		fmt.Println("    api/db.go          Get()                   (RLock → index.Lookup)")
		fmt.Println("    engine/index.go    Lookup()                (Bloom → binary search)")
		fmt.Println("    engine/storage.go  ReadDocAt()             (pread + CRC32)")
	} else {
		fmt.Println("  Breakpoints — slow-path startup (WAL rebuild):")
		fmt.Println("    api/db.go          Open()                  (missing index branch)")
		fmt.Println("    api/db.go          rebuildFromWAL()        (calls ReplayWAL)")
		fmt.Println("    engine/recovery.go ReplayWAL()             (record-by-record scan)")
		fmt.Println("    engine/wal.go      readRecord()            (header + CRC32)")
		fmt.Println()
		fmt.Println("  After startup index.json is saved — then same Get() path as above.")
	}
	fmt.Println("─────────────────────────────────────────────────")
}
