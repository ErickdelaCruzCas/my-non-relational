// Package engine — Fase 5a.5: AVL Tree
//
// An AVL Tree is a self-balancing BST that maintains the invariant:
//
//	|height(left) - height(right)| ≤ 1  for every node
//
// This guarantees O(log N) worst-case for Insert, Delete, and Range queries —
// unlike the naïve BST in bst.go which degenerates to O(N) with sorted input.
//
// # The arc: BST → AVL → Skip List
//
//	BST naïve   O(N) worst case with ordered data. Simple to implement.
//	AVL Tree    O(log N) guaranteed. Requires 4 rotation cases (LL, RR, LR, RL).
//	Skip List   O(log N) probabilistic. No rotations. Simpler code than AVL.
//
// The AVL Tree exists in this project to make the Skip List's value proposition
// concrete: "same asymptotic guarantee as AVL, but without the rotation
// bookkeeping." Without this middle step, the BST→Skip List jump feels arbitrary.
//
// Trade-offs vs. Skip List:
//   - AVL: deterministic height ≤ 1.44·log₂(N); cache-friendly for small N
//     (nodes in a balanced tree are accessed in a predictable pattern).
//   - Skip List: probabilistic O(log N); simpler code; trivially supports
//     in-order traversal at level 0; no rotations to reason about.
//   - For N > ~10,000 with range queries, the Skip List wins in practice
//     (less pointer chasing, simpler invariants under concurrent access).
//
// This file stores (key float64, offset int64) pairs, matching the interface
// of bst.go and skiplist.go so they can be swapped as range index backends.
//
// Verification test: insert 10,000 values in ascending order.
// Expected height ≤ 1.44 * log2(10000) ≈ 19.  BST naïve would be 9999.
package engine

// avlNode is an internal node of the AVL tree.
type avlNode struct {
	key     float64
	offsets []int64 // multiple docs can share the same key value
	left    *avlNode
	right   *avlNode
	height  int
}

// AVLTree is a self-balancing binary search tree keyed on float64.
// Duplicate keys are allowed; all offsets for a key are stored in one node.
type AVLTree struct {
	root *avlNode
	size int // total number of (key, offset) pairs
}

// NewAVLTree returns an empty AVL tree.
func NewAVLTree() *AVLTree {
	return &AVLTree{}
}

// Insert adds (key, offset) to the tree. Duplicate (key, offset) pairs are
// silently ignored. The tree rebalances automatically via rotations.
func (t *AVLTree) Insert(key float64, offset int64) {
	inserted := false
	t.root = avlInsert(t.root, key, offset, &inserted)
	if inserted {
		t.size++
	}
}

// Delete removes one (key, offset) pair from the tree. If the node for key
// has other offsets remaining, the node stays; otherwise the node is removed
// and the tree rebalances.
func (t *AVLTree) Delete(key float64, offset int64) {
	deleted := false
	t.root = avlDelete(t.root, key, offset, &deleted)
	if deleted {
		t.size--
	}
}

// Range returns all offsets for keys in [min, max] (inclusive) via in-order traversal.
func (t *AVLTree) Range(min, max float64) []int64 {
	var result []int64
	avlRange(t.root, min, max, &result)
	return result
}

// GreaterThan returns all offsets for keys > min.
func (t *AVLTree) GreaterThan(min float64) []int64 {
	var result []int64
	avlRange(t.root, min+1e-15, math64MaxFloat, &result)
	return result
}

// LessThan returns all offsets for keys < max.
func (t *AVLTree) LessThan(max float64) []int64 {
	var result []int64
	avlRange(t.root, math64MinFloat, max-1e-15, &result)
	return result
}

// Height returns the height of the tree root (0 for empty tree).
// For N nodes, a balanced AVL tree has height ≤ 1.44·log₂(N+2) − 1.
func (t *AVLTree) Height() int {
	return nodeHeight(t.root)
}

// Size returns the total number of (key, offset) pairs in the tree.
func (t *AVLTree) Size() int {
	return t.size
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// Sentinel float64 values for range queries.
const (
	math64MaxFloat = 1.7976931348623157e+308
	math64MinFloat = -1.7976931348623157e+308
)

func nodeHeight(n *avlNode) int {
	if n == nil {
		return 0
	}
	return n.height
}

func updateHeight(n *avlNode) {
	lh := nodeHeight(n.left)
	rh := nodeHeight(n.right)
	if lh > rh {
		n.height = lh + 1
	} else {
		n.height = rh + 1
	}
}

func balanceFactor(n *avlNode) int {
	if n == nil {
		return 0
	}
	return nodeHeight(n.left) - nodeHeight(n.right)
}

// ── Rotations ─────────────────────────────────────────────────────────────────
//
// Four cases arise when a node becomes unbalanced (|bf| > 1):
//
//	LL: left child is left-heavy  → single right rotation
//	RR: right child is right-heavy → single left rotation
//	LR: left child is right-heavy  → left rotate left child, then right rotate
//	RL: right child is left-heavy  → right rotate right child, then left rotate

// rotateRight performs a right rotation around node y:
//
//	    y                 x
//	   / \               / \
//	  x   T3    →      T1   y
//	 / \                   / \
//	T1  T2               T2  T3
func rotateRight(y *avlNode) *avlNode {
	x := y.left
	t2 := x.right

	x.right = y
	y.left = t2

	updateHeight(y)
	updateHeight(x)
	return x
}

// rotateLeft performs a left rotation around node x:
//
//	  x                   y
//	 / \                 / \
//	T1   y      →       x   T3
//	    / \            / \
//	   T2  T3        T1  T2
func rotateLeft(x *avlNode) *avlNode {
	y := x.right
	t2 := y.left

	y.left = x
	x.right = t2

	updateHeight(x)
	updateHeight(y)
	return y
}

// rebalance checks the balance factor and applies the appropriate rotation(s).
func rebalance(n *avlNode) *avlNode {
	updateHeight(n)
	bf := balanceFactor(n)

	// LL case: left subtree is 2 higher and left child is left-heavy or balanced.
	if bf > 1 && balanceFactor(n.left) >= 0 {
		logger.Info("[avl] rotation", "type", "LL", "key", n.key, "new_height", n.height-1)
		return rotateRight(n)
	}
	// LR case: left subtree is 2 higher and left child is right-heavy.
	if bf > 1 && balanceFactor(n.left) < 0 {
		logger.Info("[avl] rotation", "type", "LR", "key", n.key, "new_height", n.height-1)
		n.left = rotateLeft(n.left)
		return rotateRight(n)
	}
	// RR case: right subtree is 2 higher and right child is right-heavy or balanced.
	if bf < -1 && balanceFactor(n.right) <= 0 {
		logger.Info("[avl] rotation", "type", "RR", "key", n.key, "new_height", n.height-1)
		return rotateLeft(n)
	}
	// RL case: right subtree is 2 higher and right child is left-heavy.
	if bf < -1 && balanceFactor(n.right) > 0 {
		logger.Info("[avl] rotation", "type", "RL", "key", n.key, "new_height", n.height-1)
		n.right = rotateRight(n.right)
		return rotateLeft(n)
	}

	return n // already balanced
}

// avlInsert inserts (key, offset) into the subtree rooted at n and returns
// the new root after rebalancing.
func avlInsert(n *avlNode, key float64, offset int64, inserted *bool) *avlNode {
	if n == nil {
		*inserted = true
		logger.Debug("[avl] insert", "key", key, "offset", offset, "action", "new_node")
		return &avlNode{key: key, offsets: []int64{offset}, height: 1}
	}

	switch {
	case key < n.key:
		n.left = avlInsert(n.left, key, offset, inserted)
	case key > n.key:
		n.right = avlInsert(n.right, key, offset, inserted)
	default:
		// Same key: append offset if not already present.
		for _, o := range n.offsets {
			if o == offset {
				return n // duplicate (key, offset) pair — ignore
			}
		}
		n.offsets = append(n.offsets, offset)
		*inserted = true
		logger.Debug("[avl] insert", "key", key, "offset", offset, "action", "append_offset")
		return n // no structural change; no rebalance needed
	}

	return rebalance(n)
}

// avlDelete removes one (key, offset) pair from the subtree rooted at n.
// If the node for key has no more offsets, the node is removed using
// the in-order successor strategy.
func avlDelete(n *avlNode, key float64, offset int64, deleted *bool) *avlNode {
	if n == nil {
		return nil
	}

	switch {
	case key < n.key:
		n.left = avlDelete(n.left, key, offset, deleted)
	case key > n.key:
		n.right = avlDelete(n.right, key, offset, deleted)
	default:
		// Found the node. Remove the specific offset.
		newOffsets := n.offsets[:0]
		for _, o := range n.offsets {
			if o == offset && !*deleted {
				*deleted = true // remove exactly one occurrence
				logger.Debug("[avl] delete", "key", key, "offset", offset, "found", true)
				continue
			}
			newOffsets = append(newOffsets, o)
		}
		n.offsets = newOffsets

		if len(n.offsets) > 0 {
			return n // node still has offsets; no structural change
		}

		// Node is now empty: remove it from the tree.
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}

		// Node has two children: replace with in-order successor (leftmost of right subtree).
		succ := avlMin(n.right)
		n.key = succ.key
		n.offsets = succ.offsets
		// Remove the successor from the right subtree (it has no left child by definition).
		var dummy bool
		n.right = avlDeleteMin(n.right, &dummy)
	}

	return rebalance(n)
}

// avlMin returns the leftmost (minimum key) node in the subtree.
func avlMin(n *avlNode) *avlNode {
	for n.left != nil {
		n = n.left
	}
	return n
}

// avlDeleteMin removes the leftmost node from the subtree and returns the new root.
func avlDeleteMin(n *avlNode, deleted *bool) *avlNode {
	if n.left == nil {
		*deleted = true
		return n.right
	}
	n.left = avlDeleteMin(n.left, deleted)
	return rebalance(n)
}

// avlRange collects all offsets for nodes with keys in [min, max].
// In-order traversal with early exit (pruning) for efficiency.
func avlRange(n *avlNode, min, max float64, result *[]int64) {
	if n == nil {
		return
	}
	// Prune left subtree if all its keys are < min.
	if n.key >= min {
		avlRange(n.left, min, max, result)
	}
	// Visit this node if in range.
	if n.key >= min && n.key <= max {
		*result = append(*result, n.offsets...)
	}
	// Prune right subtree if all its keys are > max.
	if n.key <= max {
		avlRange(n.right, min, max, result)
	}
}
