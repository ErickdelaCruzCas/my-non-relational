// Package views provides terminal formatting for the REPL.
//
// Colors are enabled only when stdout is a real TTY. When output is piped
// or redirected, all functions emit plain text with no ANSI codes.
package views

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// isTTY is set once at startup. False means stdout is a pipe or file.
var isTTY bool

func init() {
	if fi, err := os.Stdout.Stat(); err == nil {
		isTTY = fi.Mode()&os.ModeCharDevice != 0
	}
}

// ANSI escape codes.
const (
	reset = "\033[0m"
	bold  = "\033[1m"
	red   = "\033[31m"
	green = "\033[32m"
	cyan  = "\033[36m"
)

func color(code, s string) string {
	if !isTTY {
		return s
	}
	return code + s + reset
}

// Prompt prints the interactive prompt without a trailing newline.
func Prompt() {
	fmt.Print(color(bold, ">") + " ")
}

// Success prints a green confirmation message.
func Success(msg string) {
	fmt.Println(color(green, "✓ "+msg))
}

// Error prints a red error message.
func Error(err error) {
	fmt.Println(color(red, "✗ error: "+err.Error()))
}

// ID prints a single document ID in cyan.
func ID(id string) {
	fmt.Println(color(cyan, id))
}

// Doc prints a document as indented JSON in cyan.
// Fields are sorted alphabetically, with _id always first.
func Doc(doc map[string]any) {
	out, err := marshalSorted(doc)
	if err != nil {
		fmt.Println(color(red, "✗ error serializing doc: "+err.Error()))
		return
	}
	fmt.Println(color(cyan, string(out)))
}

// Help prints a formatted table of all available REPL commands.
func Help() {
	h := color(bold, "COMMANDS")
	fmt.Println(h)
	rows := []struct{ cmd, desc, example string }{
		{"insert <json>", "Inserta un documento.", `e.g. insert {"name":"alice"}`},
		{"get <id>", "Obtiene un documento.", ""},
		{"update <id> <json>", "Fusiona campos en un documento existente.", ""},
		{"find", "Lista todos los IDs.", ""},
		{"find <campo>=<valor>", "Lista IDs donde campo coincide con valor.", ""},
		{"delete <id>", "Elimina un documento.", ""},
		{"help", "Muestra este mensaje.", ""},
		{"exit", "Salir.", ""},
	}
	for _, r := range rows {
		if r.example != "" {
			fmt.Printf("  %-26s %-40s %s\n", r.cmd, r.desc, color(cyan, r.example))
		} else {
			fmt.Printf("  %-26s %s\n", r.cmd, r.desc)
		}
	}
}

// Banner prints the startup header.
func Banner(phase string) {
	fmt.Println(color(bold, "my-non-relational") + " — " + phase)
	fmt.Println(`type ` + color(cyan, "help") + ` to see available commands`)
}

// marshalSorted serializes doc as indented JSON with _id first, then other
// fields alphabetically. This gives deterministic, readable output.
func marshalSorted(doc map[string]any) ([]byte, error) {
	ordered := make([]struct {
		k string
		v any
	}, 0, len(doc))

	// _id always first.
	if id, ok := doc["_id"]; ok {
		ordered = append(ordered, struct {
			k string
			v any
		}{"_id", id})
	}
	keys := make([]string, 0, len(doc)-1)
	for k := range doc {
		if k != "_id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		ordered = append(ordered, struct {
			k string
			v any
		}{k, doc[k]})
	}

	// Encode to a map preserving insertion order via intermediate JSON.
	// Go's encoding/json doesn't guarantee map key order, so we build the
	// JSON string manually for the outer object.
	buf := []byte("{\n")
	for i, kv := range ordered {
		keyBytes, _ := json.Marshal(kv.k)
		valBytes, err := json.MarshalIndent(kv.v, "  ", "  ")
		if err != nil {
			return nil, err
		}
		buf = append(buf, []byte("  "+string(keyBytes)+": "+string(valBytes))...)
		if i < len(ordered)-1 {
			buf = append(buf, ',')
		}
		buf = append(buf, '\n')
	}
	buf = append(buf, '}')
	return buf, nil
}
