// Package engine — query pipeline for Phase 4.
//
// # Fase 4 — Motor de consultas
//
// FindRequest is the typed query object that replaces the old map[string]string
// filter. ExecuteFind runs the pipeline:
//
//  1. Strategy: single eq-filter → secondary index (O(k) reads).
//     Otherwise         → full primary-index scan (O(N) reads).
//  2. Filter remaining docs against all Filters.
//  3. Sort + Limit: if both SortBy and Limit are set → Min-Heap TopK O(N log K).
//     If only SortBy (no limit)                     → collect all, sort O(N log N).
//     If only Limit (no sort)                       → collect first K.
//  4. Projection: keep only requested fields (_id always included).
package engine

import (
	"fmt"
	"os"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// Filter represents a single predicate field op value.
//
// Supported Ops:
//
//	"eq"      — equality (Phase 4, secondary index)
//	"gt"      — greater than (Phase 5a, BST)
//	"gte"     — greater than or equal
//	"lt"      — less than
//	"lte"     — less than or equal
//	"between" — Value must be [2]float64{lo, hi}, inclusive on both ends
type Filter struct {
	Field string
	Op    string
	Value any
}

// FindRequest is the typed query passed to DB.Find.
type FindRequest struct {
	Filters    []Filter // empty = match all documents
	SortBy     string   // field name; "" = no sort
	SortAsc    bool     // true = ascending, false = descending
	Limit      int      // 0 = no limit
	Projection []string // nil = all fields; _id is always included
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// ExecuteFind runs the query pipeline on a disk-backed collection.
// entries is the full sorted primary index. secondary is used for single-eq
// optimization. rangeIdx is used for single range-op optimization (Phase 5a).
// file is the WAL *os.File. ser is the active serializer.
func ExecuteFind(entries []IndexEntry, secondary *SecondaryIndex, rangeIdx *RangeAVLIndex, file *os.File, ser Serializer, req FindRequest) ([]map[string]any, error) {
	// ── 1. Strategy: pick candidate offsets ──────────────────────────────────
	var candidates []map[string]any
	strategy := "full_scan"

	if len(req.Filters) == 1 {
		f := req.Filters[0]
		if f.Op == "eq" {
			offsets := secondary.Lookup(f.Field, fmt.Sprintf("%v", f.Value))
			strategy = "secondary"
			LogInfo("[query] strategy", "type", strategy, "field", f.Field, "value", f.Value, "candidates", len(offsets))
			for _, off := range offsets {
				doc, err := ReadDocAt(file, off, ser)
				if err != nil {
					LogInfo("[query] read_error", "offset", off, "err", err)
					continue
				}
				candidates = append(candidates, doc)
			}
		} else if isRangeOp(f.Op) {
			offsets := rangeIdx.Query(f.Field, f.Op, f.Value)
			strategy = "range_bst"
			LogInfo("[query] strategy", "type", strategy, "field", f.Field, "op", f.Op, "value", f.Value, "candidates", len(offsets))
			for _, off := range offsets {
				doc, err := ReadDocAt(file, off, ser)
				if err != nil {
					LogInfo("[query] read_error", "offset", off, "err", err)
					continue
				}
				candidates = append(candidates, doc)
			}
		} else {
			LogInfo("[query] strategy", "type", strategy, "total_docs", len(entries))
			for _, e := range entries {
				doc, err := ReadDocAt(file, e.Offset, ser)
				if err != nil {
					LogInfo("[query] read_error", "id", e.ID, "err", err)
					continue
				}
				candidates = append(candidates, doc)
			}
		}
	} else {
		LogInfo("[query] strategy", "type", strategy, "total_docs", len(entries))
		for _, e := range entries {
			doc, err := ReadDocAt(file, e.Offset, ser)
			if err != nil {
				LogInfo("[query] read_error", "id", e.ID, "err", err)
				continue
			}
			candidates = append(candidates, doc)
		}
	}

	// ── 2. Filter ─────────────────────────────────────────────────────────────
	var matched []map[string]any
	for _, doc := range candidates {
		if matchesAll(doc, req.Filters) {
			matched = append(matched, doc)
		}
	}
	LogInfo("[query] filter", "strategy", strategy, "candidates", len(candidates), "matched", len(matched))

	// ── 3. Sort + Limit ───────────────────────────────────────────────────────
	var result []map[string]any

	switch {
	case req.SortBy != "" && req.Limit > 0:
		// TopK with min-heap: O(N log K)
		result = topK(matched, req.SortBy, req.SortAsc, req.Limit)

	case req.SortBy != "":
		// Full sort, no limit: O(N log N)
		result = sortAll(matched, req.SortBy, req.SortAsc)

	case req.Limit > 0:
		// Limit without sort: take first K
		if req.Limit < len(matched) {
			result = matched[:req.Limit]
		} else {
			result = matched
		}

	default:
		result = matched
	}

	// ── 4. Projection ─────────────────────────────────────────────────────────
	if len(req.Projection) > 0 {
		result = project(result, req.Projection)
	}

	return result, nil
}

// ── Sort helpers ──────────────────────────────────────────────────────────────

// topK returns the K docs with the highest (asc=false) or lowest (asc=true)
// value in sortField using a min-heap of capacity K. O(N log K).
func topK(docs []map[string]any, sortField string, asc bool, k int) []map[string]any {
	h := NewMinHeap(k)
	for _, doc := range docs {
		v := toFloat(doc[sortField])
		// For DESC (largest K): min-heap stores largest seen, ejects minimum.
		// For ASC (smallest K): negate values — min-heap over negated domain
		//   keeps the K smallest originals (= K largest negated values).
		val := v
		if asc {
			val = -v
		}
		item := heapItem{doc: doc, value: val}
		if h.Len() < k {
			h.Push(item)
		} else if val > h.Min().value {
			h.Pop()
			h.Push(item)
		}
	}

	// Drain gives ascending order of stored values.
	drained := h.Drain()
	out := make([]map[string]any, len(drained))
	if asc {
		// Stored as negated → drain gives least-negative first = largest original first.
		// Reverse to get ascending original order.
		for i, item := range drained {
			out[len(drained)-1-i] = item.doc
		}
	} else {
		// Stored as-is → drain gives smallest-first = least-preferred first.
		// Reverse to get largest first (desc order).
		for i, item := range drained {
			out[len(drained)-1-i] = item.doc
		}
	}
	return out
}

// sortAll sorts all docs by sortField without a limit. O(N log N).
// Implemented with insertion sort for clarity (N is small in this phase).
// TODO Phase 9: replace with merge sort or quicksort for large N.
func sortAll(docs []map[string]any, sortField string, asc bool) []map[string]any {
	out := make([]map[string]any, len(docs))
	copy(out, docs)
	// Insertion sort: simple, in-place, O(N²) worst case.
	for i := 1; i < len(out); i++ {
		key := out[i]
		kv := toFloat(key[sortField])
		j := i - 1
		for j >= 0 {
			jv := toFloat(out[j][sortField])
			if asc && jv <= kv {
				break
			}
			if !asc && jv >= kv {
				break
			}
			out[j+1] = out[j]
			j--
		}
		out[j+1] = key
	}
	return out
}

// ── Filter helper ─────────────────────────────────────────────────────────────

// MatchesAllFilters reports whether doc satisfies all filters.
// Exported so api/db.go can use it for the in-memory mode path.
func MatchesAllFilters(doc map[string]any, filters []Filter) bool {
	return matchesAll(doc, filters)
}

// matchesAll reports whether doc satisfies all filters.
func matchesAll(doc map[string]any, filters []Filter) bool {
	for _, f := range filters {
		val, ok := doc[f.Field]
		if !ok {
			return false
		}
		switch f.Op {
		case "eq":
			if fmt.Sprintf("%v", val) != fmt.Sprintf("%v", f.Value) {
				return false
			}
		case "gt":
			if !(toFloat(val) > toFloat(f.Value)) {
				return false
			}
		case "gte":
			if !(toFloat(val) >= toFloat(f.Value)) {
				return false
			}
		case "lt":
			if !(toFloat(val) < toFloat(f.Value)) {
				return false
			}
		case "lte":
			if !(toFloat(val) <= toFloat(f.Value)) {
				return false
			}
		case "between":
			bounds := f.Value.([2]float64)
			v := toFloat(val)
			if !(v >= bounds[0] && v <= bounds[1]) {
				return false
			}
		}
	}
	return true
}

// isRangeOp reports whether op is a range predicate (Phase 5a).
func isRangeOp(op string) bool {
	switch op {
	case "gt", "gte", "lt", "lte", "between":
		return true
	}
	return false
}

// ── Projection helper ─────────────────────────────────────────────────────────

// project returns docs with only the requested fields. _id is always included.
func project(docs []map[string]any, fields []string) []map[string]any {
	keep := make(map[string]bool, len(fields)+1)
	keep["_id"] = true
	for _, f := range fields {
		keep[f] = true
	}
	out := make([]map[string]any, len(docs))
	for i, doc := range docs {
		projected := make(map[string]any, len(keep))
		for k, v := range doc {
			if keep[k] {
				projected[k] = v
			}
		}
		out[i] = projected
	}
	return out
}

// ── Numeric coercion ──────────────────────────────────────────────────────────

// toFloat converts a document field value to float64 for comparison.
// JSON numbers decode as float64; booleans become 0/1.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return 0
}
