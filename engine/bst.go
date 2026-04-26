// Package engine — naive (unbalanced) BST for range queries.
//
// # Fase 5a — BST Naïve
//
// This BST is intentionally unbalanced. Inserting N documents in sorted key
// order degrades to a linked list: O(N) lookup, O(N) range scan worst case.
// That is the educational point — it motivates Phase 5a.5 (AVL Tree, which
// guarantees O(log N) with rotations) and Phase 5b (Skip List, which achieves
// O(log N) probabilistically without rotations).
//
// Contrast with engine/avl.go (already implemented for Phase 5a.5): the AVL
// tree keeps the same public interface (Insert/Remove/Range) but adds height
// tracking and up to 4 rotation cases to maintain balance.
//
// RangeIndex wraps one BST per indexed field. Only numeric document fields
// (float64, int, int64, bool) are indexed; string fields are skipped.
//
// Index lifecycle:
//
//	Insert doc  → RangeIndex.AddDoc(doc, offset)
//	Update doc  → RangeIndex.RemoveDoc(oldDoc, oldOffset) + AddDoc(newDoc, newOffset)
//	Delete doc  → RangeIndex.RemoveDoc(doc, offset)
//	Startup     → RangeIndex.AddDoc called per live doc during WAL replay
//	Persistence → RangeIndex.All() serializes to index.json; loaded via Insert on Open
package engine

import "math"

// ── BST node ──────────────────────────────────────────────────────────────────

// bstNode is one key slot in the BST.
// Multiple documents can share the same numeric key value; their offsets are
// stored together in the slice.
type bstNode struct {
	key     float64
	offsets []int64 // WAL offsets of docs whose indexed field equals key
	left    *bstNode
	right   *bstNode
}

// ── BST ───────────────────────────────────────────────────────────────────────

// BST is an unbalanced binary search tree keyed on float64.
// Not thread-safe; callers hold the appropriate lock (db.mu).
type BST struct {
	root *bstNode
	size int // number of distinct key values currently stored
}

// newBST returns an empty BST.
func newBST() *BST { return &BST{} }

// Insert adds offset to the node with the given key.
// If no node with that key exists, a new node is created (standard BST insert).
// O(h) where h is tree height — O(log N) average, O(N) worst case (sorted input).
func (b *BST) Insert(key float64, offset int64) {
	b.root = bstInsert(b.root, key, offset, &b.size)
}

func bstInsert(node *bstNode, key float64, offset int64, size *int) *bstNode {
	if node == nil {
		*size++
		return &bstNode{key: key, offsets: []int64{offset}}
	}
	if key < node.key {
		node.left = bstInsert(node.left, key, offset, size)
	} else if key > node.key {
		node.right = bstInsert(node.right, key, offset, size)
	} else {
		// Key already exists — append offset to the existing slice.
		node.offsets = append(node.offsets, offset)
	}
	return node
}

// Remove removes offset from the node with the given key.
// If the node's offset list becomes empty after removal, the node is deleted
// from the tree. Two-child deletion uses the in-order successor (leftmost node
// of the right subtree) as the replacement — classic BST strategy, no rotations.
// O(h) time.
func (b *BST) Remove(key float64, offset int64) {
	b.root = bstRemove(b.root, key, offset, &b.size)
}

func bstRemove(node *bstNode, key float64, offset int64, size *int) *bstNode {
	if node == nil {
		return nil
	}
	if key < node.key {
		node.left = bstRemove(node.left, key, offset, size)
		return node
	}
	if key > node.key {
		node.right = bstRemove(node.right, key, offset, size)
		return node
	}

	// Found the node — remove the specific offset from its slice.
	node.offsets = removeOffset(node.offsets, offset)
	if len(node.offsets) > 0 {
		return node // other offsets still live here; keep node
	}

	// No offsets left — delete this node from the tree.
	*size--
	if node.left == nil {
		return node.right
	}
	if node.right == nil {
		return node.left
	}
	// Two children: replace with in-order successor (min of right subtree).
	succ := bstMin(node.right)
	node.key = succ.key
	node.offsets = succ.offsets
	// Delete the successor from the right subtree.
	node.right = bstDeleteMin(node.right)
	*size-- // bstDeleteMin removed a node; correct the double-decrement above
	*size++ // (successor's offsets moved up, not deleted)
	return node
}

// removeOffset returns s with the first occurrence of offset removed.
func removeOffset(s []int64, offset int64) []int64 {
	for i, v := range s {
		if v == offset {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// bstMin returns the node with the smallest key in the subtree rooted at node.
func bstMin(node *bstNode) *bstNode {
	for node.left != nil {
		node = node.left
	}
	return node
}

// bstDeleteMin removes the node with the smallest key from the subtree and
// returns the new root of that subtree.
func bstDeleteMin(node *bstNode) *bstNode {
	if node.left == nil {
		return node.right
	}
	node.left = bstDeleteMin(node.left)
	return node
}

// Range collects all WAL offsets for keys in [lo, hi] (bounds controlled by
// inclLo and inclHi). Uses in-order traversal with subtree pruning:
//   - skip left subtree entirely if all its keys are outside [lo, hi]
//   - skip right subtree entirely if all its keys are outside [lo, hi]
//
// O(k + h) where k is the number of matching offsets and h is tree height.
func (b *BST) Range(lo, hi float64, inclLo, inclHi bool) []int64 {
	var out []int64
	bstRange(b.root, lo, hi, inclLo, inclHi, &out)
	return out
}

func bstRange(node *bstNode, lo, hi float64, inclLo, inclHi bool, out *[]int64) {
	if node == nil {
		return
	}

	// Prune: entire left subtree is ≤ node.key.
	// Visit left only if node.key > lo (there might be matching keys to the left).
	aboveLo := inclLo && node.key >= lo || !inclLo && node.key > lo
	if aboveLo {
		bstRange(node.left, lo, hi, inclLo, inclHi, out)
	}

	// Visit this node if its key is within [lo, hi].
	inRange := (inclLo && node.key >= lo || !inclLo && node.key > lo) &&
		(inclHi && node.key <= hi || !inclHi && node.key < hi)
	if inRange {
		*out = append(*out, node.offsets...)
	}

	// Prune: entire right subtree is ≥ node.key.
	// Visit right only if node.key < hi (there might be matching keys to the right).
	belowHi := inclHi && node.key <= hi || !inclHi && node.key < hi
	if belowHi {
		bstRange(node.right, lo, hi, inclLo, inclHi, out)
	}
}

// Len returns the number of distinct key values in the BST.
func (b *BST) Len() int { return b.size }

// ── RangeIndex ────────────────────────────────────────────────────────────────

// RangeIndex maintains one BST per indexed field. Only numeric document
// fields are stored; string fields are skipped silently.
type RangeIndex struct {
	trees map[string]*BST // field name → BST
}

// NewRangeIndex returns an empty RangeIndex.
func NewRangeIndex() *RangeIndex {
	return &RangeIndex{trees: make(map[string]*BST)}
}

// AddDoc indexes all numeric fields of doc at the given WAL offset.
func (r *RangeIndex) AddDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		if !isNumeric(val) {
			continue
		}
		tree := r.treeFor(field)
		tree.Insert(toFloat(val), offset)
	}
}

// RemoveDoc removes all numeric fields of doc at offset from the index.
func (r *RangeIndex) RemoveDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		if !isNumeric(val) {
			continue
		}
		if tree, ok := r.trees[field]; ok {
			tree.Remove(toFloat(val), offset)
		}
	}
}

// Query returns WAL offsets satisfying (field op value).
//
//	"gt"      → field > value
//	"gte"     → field >= value
//	"lt"      → field < value
//	"lte"     → field <= value
//	"between" → value is [2]float64{lo, hi} — field in [lo, hi] inclusive
func (r *RangeIndex) Query(field, op string, value any) []int64 {
	tree, ok := r.trees[field]
	if !ok {
		return nil
	}
	inf := math.Inf(1)
	ninf := math.Inf(-1)

	switch op {
	case "gt":
		return tree.Range(toFloat(value), inf, false, true)
	case "gte":
		return tree.Range(toFloat(value), inf, true, true)
	case "lt":
		return tree.Range(ninf, toFloat(value), true, false)
	case "lte":
		return tree.Range(ninf, toFloat(value), true, true)
	case "between":
		bounds := value.([2]float64)
		return tree.Range(bounds[0], bounds[1], true, true)
	}
	return nil
}

// All serializes the RangeIndex for persistence.
// Returns: field → list of [key, offsets] pairs where offsets is []int64.
// The JSON representation is: { "score": [[10.0, [12, 48]], [20.0, [36]]], ... }
func (r *RangeIndex) All() map[string][][2]any {
	out := make(map[string][][2]any, len(r.trees))
	for field, tree := range r.trees {
		var entries [][2]any
		bstCollect(tree.root, &entries)
		if len(entries) > 0 {
			out[field] = entries
		}
	}
	return out
}

// bstCollect does an in-order traversal and appends [key, offsets] pairs.
func bstCollect(node *bstNode, out *[][2]any) {
	if node == nil {
		return
	}
	bstCollect(node.left, out)
	*out = append(*out, [2]any{node.key, node.offsets})
	bstCollect(node.right, out)
}

// LoadEntry inserts a single (key, offset) pair into the field's BST.
// Called during index.json loading.
func (r *RangeIndex) LoadEntry(field string, key float64, offset int64) {
	r.treeFor(field).Insert(key, offset)
}

// MaxTreeHeight returns the height of the BST for the given field.
// Used by the BST vs AVL contrast test (TestAVLBalanceVsBST).
// On sorted inserts, height ≈ N — the degenerate linked-list case.
func (r *RangeIndex) MaxTreeHeight(field string) int {
	if t, ok := r.trees[field]; ok {
		return bstHeight(t.root)
	}
	return 0
}

// bstHeight returns the height of the subtree rooted at n. O(N) traversal.
func bstHeight(n *bstNode) int {
	if n == nil {
		return 0
	}
	l, r := bstHeight(n.left), bstHeight(n.right)
	if l > r {
		return l + 1
	}
	return r + 1
}

// treeFor returns the BST for field, creating it if it does not exist.
func (r *RangeIndex) treeFor(field string) *BST {
	if t, ok := r.trees[field]; ok {
		return t
	}
	t := newBST()
	r.trees[field] = t
	return t
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isNumeric reports whether v can be meaningfully stored in the range index.
// Strings are excluded — range queries on strings are not supported in Phase 5a.
func isNumeric(v any) bool {
	switch v.(type) {
	case float64, int, int64, bool:
		return true
	}
	return false
}
