package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"my-non-relational/api"
	"my-non-relational/cmd/views"
	"my-non-relational/engine"
)

func main() {
	const dbPath = "data/"
	engine.LogInfo("[repl] start", "path", dbPath)
	db, err := api.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	views.Banner("Phase 1 (in-memory)")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		views.Prompt()
		if !scanner.Scan() {
			break // EOF (Ctrl+D)
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		handleCommand(db, line)
	}
}

func handleCommand(db *api.DB, line string) {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])
	rest := ""
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}

	// Truncate args in the log to avoid leaking large JSON payloads.
	logArgs := rest
	if len(logArgs) > 80 {
		logArgs = logArgs[:80] + "..."
	}
	engine.LogInfo("[repl] cmd", "name", cmd, "args", logArgs)

	switch cmd {
	case "insert":
		cmdInsert(db, rest)
	case "get":
		cmdGet(db, rest)
	case "update":
		cmdUpdate(db, rest)
	case "delete":
		cmdDelete(db, rest)
	case "help":
		views.Help()
	case "exit":
		views.Success("bye")
		os.Exit(0)
	default:
		fmt.Printf("unknown command %q — ", cmd)
		views.Help()
	}
}

func cmdInsert(db *api.DB, rest string) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(rest), &doc); err != nil {
		views.Error(fmt.Errorf("invalid JSON: %w", err))
		return
	}
	id, err := db.Insert(doc)
	if err != nil {
		views.Error(err)
		return
	}
	views.Success("inserted  " + id)
}

func cmdGet(db *api.DB, id string) {
	if id == "" {
		views.Error(fmt.Errorf("usage: get <id>"))
		return
	}
	doc, err := db.Get(id)
	if err != nil {
		views.Error(err)
		return
	}
	views.Doc(doc)
}

func cmdUpdate(db *api.DB, rest string) {
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		views.Error(fmt.Errorf("usage: update <id> <json>"))
		return
	}
	id := strings.TrimSpace(parts[0])
	var partial map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &partial); err != nil {
		views.Error(fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if err := db.Update(id, partial); err != nil {
		views.Error(err)
		return
	}
	views.Success("updated  " + id)
}

func cmdDelete(db *api.DB, id string) {
	if id == "" {
		views.Error(fmt.Errorf("usage: delete <id>"))
		return
	}
	if err := db.Delete(id); err != nil {
		views.Error(err)
		return
	}
	views.Success("deleted  " + id)
}
