// Package engine — AVL-backed range index for Phase 5a.5.
//
// RangeAVLIndex is a drop-in replacement for RangeIndex (bst.go) that uses
// the self-balancing AVLTree instead of the naive BST.
//
// # Why this exists
//
// Phase 5a demonstrated that inserting N documents in sorted key order degrades
// the naive BST to a linked list: O(N) range queries. RangeAVLIndex fixes that
// by using the AVL tree, which guarantees O(log N) through 4 rotation cases.
//
// The contrast is made concrete by TestAVLBalanceVsBST (tests/phase5a5_test.go):
// 200 sorted inserts → BST height ≈ 200, AVL height ≤ 15.
//
// # Interface
//
// Public methods match RangeIndex exactly so api/db.go only needs two lines
// changed (type declaration + constructor call).
//
// # Serialization
//
// All() emits the same [[key, offsets], ...] format as RangeIndex.All() so
// index.json is forward-compatible between the two backends.
package engine

// RangeAVLIndex maintains one AVLTree per indexed field.
// Only numeric document fields are indexed; string fields are skipped silently.
// Not thread-safe; callers hold the appropriate lock (db.mu).
type RangeAVLIndex struct {
	trees map[string]*AVLTree // field name → AVL tree
}

// NewRangeAVLIndex returns an empty RangeAVLIndex.
func NewRangeAVLIndex() *RangeAVLIndex {
	return &RangeAVLIndex{trees: make(map[string]*AVLTree)}
}

// AddDoc indexes all numeric fields of doc at the given WAL offset.
// The AVL tree rebalances automatically after each insert.
func (r *RangeAVLIndex) AddDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		if !isNumeric(val) {
			continue
		}
		r.treeFor(field).Insert(toFloat(val), offset)
	}
}

// RemoveDoc removes all numeric fields of doc at offset from the index.
// The AVL tree rebalances automatically after each deletion.
func (r *RangeAVLIndex) RemoveDoc(doc map[string]any, offset int64) {
	for field, val := range doc {
		if field == "_id" {
			continue
		}
		if !isNumeric(val) {
			continue
		}
		if tree, ok := r.trees[field]; ok {
			tree.Delete(toFloat(val), offset)
		}
	}
}

// Query returns WAL offsets satisfying (field op value).
//
//	"gt"      → field > value   (exclusive lower bound via epsilon in AVLTree.GreaterThan)
//	"gte"     → field >= value  (inclusive, uses AVLTree.Range with max sentinel)
//	"lt"      → field < value   (exclusive upper bound via epsilon in AVLTree.LessThan)
//	"lte"     → field <= value  (inclusive, uses AVLTree.Range with min sentinel)
//	"between" → field in [lo, hi] inclusive; value must be [2]float64{lo, hi}
func (r *RangeAVLIndex) Query(field, op string, value any) []int64 {
	tree, ok := r.trees[field]
	if !ok {
		return nil
	}
	switch op {
	case "gt":
		return tree.GreaterThan(toFloat(value))
	case "gte":
		return tree.Range(toFloat(value), math64MaxFloat)
	case "lt":
		return tree.LessThan(toFloat(value))
	case "lte":
		return tree.Range(math64MinFloat, toFloat(value))
	case "between":
		bounds := value.([2]float64)
		return tree.Range(bounds[0], bounds[1])
	}
	return nil
}

// All serializes the RangeAVLIndex for persistence in index.json.
// Format: field → [[key, []offset], ...] — identical to RangeIndex.All().
func (r *RangeAVLIndex) All() map[string][][2]any {
	out := make(map[string][][2]any, len(r.trees))
	for field, tree := range r.trees {
		var entries [][2]any
		avlCollect(tree.root, &entries)
		if len(entries) > 0 {
			out[field] = entries
		}
	}
	return out
}

// avlCollect does an in-order traversal and appends [key, offsets] pairs.
func avlCollect(node *avlNode, out *[][2]any) {
	if node == nil {
		return
	}
	avlCollect(node.left, out)
	*out = append(*out, [2]any{node.key, node.offsets})
	avlCollect(node.right, out)
}

// LoadEntry inserts a single (key, offset) pair into the field's AVL tree.
// Called during index.json loading to rebuild the index from disk.
func (r *RangeAVLIndex) LoadEntry(field string, key float64, offset int64) {
	r.treeFor(field).Insert(key, offset)
}

// MaxTreeHeight returns the height of the AVL tree for the given field.
// Used by the BST vs AVL contrast test (TestAVLBalanceVsBST).
// For a balanced AVL tree with N nodes: height ≤ 1.44·log₂(N).
func (r *RangeAVLIndex) MaxTreeHeight(field string) int {
	if tree, ok := r.trees[field]; ok {
		return tree.Height()
	}
	return 0
}

// treeFor returns the AVL tree for field, creating it if it does not exist.
func (r *RangeAVLIndex) treeFor(field string) *AVLTree {
	if t, ok := r.trees[field]; ok {
		return t
	}
	t := NewAVLTree()
	r.trees[field] = t
	return t
}
