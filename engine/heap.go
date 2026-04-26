// Package engine — Min-Heap for TopK document sorting.
//
// # Fase 4 — Min-Heap
//
// MinHeap maintains the K documents with the highest value in a sort field.
// It is used by the query engine when both SortBy and Limit are set.
//
// Algorithm (TopK with a min-heap of capacity K):
//
//	for each document doc with value v:
//	    if heap.Len() < K         → push doc
//	    else if v > heap.Min()    → pop min, push doc
//	    else                      → skip doc
//
// At the end, the heap contains the K largest values. Pop all to get them in
// ascending order; reverse for descending.
//
// Complexity: O(N log K) time, O(K) space — far better than O(N log N) full
// sort when K << N.
//
// For DESC sort (largest K values): use as-is (min-heap keeps largest K).
// For ASC sort (smallest K values): negate the values before pushing so the
// min-heap effectively becomes a max-heap over the negated domain.
//
// Note: This same min-heap is reused in Phase 9b for K-way SSTable merge,
// where each heap item carries a (value, sourceIterator) pair instead of a doc.
package engine

// heapItem is one slot in the MinHeap.
type heapItem struct {
	doc   map[string]any
	value float64 // numeric representation of the sort field
}

// MinHeap is a binary min-heap of heapItems with a fixed capacity.
// The root is always the item with the smallest value.
// Not thread-safe; callers hold the appropriate lock.
type MinHeap struct {
	items []heapItem
	cap   int // maximum number of items
}

// NewMinHeap creates an empty MinHeap with the given capacity.
func NewMinHeap(cap int) *MinHeap {
	return &MinHeap{
		items: make([]heapItem, 0, cap),
		cap:   cap,
	}
}

// Len returns the number of items currently in the heap.
func (h *MinHeap) Len() int { return len(h.items) }

// Cap returns the maximum capacity of the heap.
func (h *MinHeap) Cap() int { return h.cap }

// Min returns the item with the smallest value without removing it.
// Panics if the heap is empty.
func (h *MinHeap) Min() heapItem { return h.items[0] }

// Push inserts item into the heap, then restores the heap property by
// sifting the new item up. O(log N).
func (h *MinHeap) Push(item heapItem) {
	h.items = append(h.items, item)
	h.siftUp(len(h.items) - 1)
}

// Pop removes and returns the item with the smallest value, then restores
// the heap property by sifting the new root down. O(log N).
func (h *MinHeap) Pop() heapItem {
	min := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	if len(h.items) > 0 {
		h.siftDown(0)
	}
	return min
}

// Drain pops all items and returns them in ascending order (smallest first).
func (h *MinHeap) Drain() []heapItem {
	out := make([]heapItem, 0, len(h.items))
	for len(h.items) > 0 {
		out = append(out, h.Pop())
	}
	return out
}

// siftUp moves the item at index i up until the heap property is restored.
// Parent of i is (i-1)/2.
func (h *MinHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[parent].value <= h.items[i].value {
			break
		}
		h.items[parent], h.items[i] = h.items[i], h.items[parent]
		i = parent
	}
}

// siftDown moves the item at index i down until the heap property is restored.
// Children of i are 2*i+1 (left) and 2*i+2 (right).
func (h *MinHeap) siftDown(i int) {
	n := len(h.items)
	for {
		smallest := i
		left := 2*i + 1
		right := 2*i + 2

		if left < n && h.items[left].value < h.items[smallest].value {
			smallest = left
		}
		if right < n && h.items[right].value < h.items[smallest].value {
			smallest = right
		}
		if smallest == i {
			break
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}
