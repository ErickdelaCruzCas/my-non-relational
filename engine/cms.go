// Package engine — Fase 8: Count-Min Sketch
//
// A Count-Min Sketch (CMS) estimates the frequency of any element in a stream
// using O(width × depth) counters. It never underestimates; it may overestimate
// by at most ε·N where ε = e/width and N is the total count of all elements.
//
// # How it works
//
// The sketch is a 2D array of counters: depth rows, each of width columns.
// Each row uses a different hash function.
//
// Add(key):
//   - For each row i, compute h_i(key) → column.
//   - Increment counters[i][col].
//
// Estimate(key):
//   - For each row i, compute h_i(key) → column.
//   - Return the minimum counter value across all rows.
//
// The minimum is the best estimate: overcounting from hash collisions can only
// inflate individual cells, never reduce them. The true count is always ≤ minimum.
//
// # Error guarantee
//
//	P(estimate > true + ε·N) ≤ δ
//	where ε = e/width,  δ = e^(-depth)
//
// Typical: width=2000, depth=7 → ε=0.14%, δ=0.09%
//
// # Use in this DB
//
// In Fase 8, the CMS tracks how often each (field, value) pair appears in
// queries. Example: "how many queries filter by city='mx'?" This feeds the
// IndexHitRatio breakdown in Explain and helps identify hot index values.
//
// Unlike HyperLogLog (which counts distinct values), CMS counts occurrences.
// They are complementary: HLL says "there are 50K distinct cities"; CMS says
// "city='mx' was queried 12,000 times in the last minute".
//
// Trade-off: exact frequency tracking uses a hash map O(distinct_values) memory;
// CMS uses O(width×depth) ≈ fixed memory regardless of stream size.
package engine

// CountMinSketch is a probabilistic frequency estimator.
// It guarantees no undercount; overcount is bounded by ε·N.
type CountMinSketch struct {
	counters [][]uint64
	width    uint
	depth    uint
}

// NewCountMinSketch creates a CMS with the given dimensions.
//
//   - width controls accuracy: ε = e/width (error fraction of total count).
//   - depth controls confidence: δ = e^(-depth) (probability of exceeding error).
//
// Suggested defaults: width=2000, depth=7 for ε≈0.14%, δ≈0.09%.
func NewCountMinSketch(width, depth uint) *CountMinSketch {
	if width == 0 {
		width = 2000
	}
	if depth == 0 {
		depth = 7
	}
	counters := make([][]uint64, depth)
	for i := range counters {
		counters[i] = make([]uint64, width)
	}
	logger.Info("[cms] created", "width", width, "depth", depth)
	return &CountMinSketch{
		counters: counters,
		width:    width,
		depth:    depth,
	}
}

// Add increments the frequency count for key.
func (c *CountMinSketch) Add(key string) {
	for i := uint(0); i < c.depth; i++ {
		col := cmsHash(key, i) % uint64(c.width)
		c.counters[i][col]++
		logger.Debug("[cms] add", "key", key, "row", i, "col", col, "new_count", c.counters[i][col])
	}
}

// AddN increments the frequency count for key by n.
func (c *CountMinSketch) AddN(key string, n uint64) {
	for i := uint(0); i < c.depth; i++ {
		col := cmsHash(key, i) % uint64(c.width)
		c.counters[i][col] += n
	}
}

// Estimate returns the estimated frequency of key.
// The true frequency is guaranteed to be ≤ the returned value.
// Overcount is bounded by ε·N with probability ≥ 1-δ.
func (c *CountMinSketch) Estimate(key string) uint64 {
	var min uint64 = ^uint64(0) // max uint64
	for i := uint(0); i < c.depth; i++ {
		col := cmsHash(key, i) % uint64(c.width)
		if c.counters[i][col] < min {
			min = c.counters[i][col]
		}
	}
	return min
}

// Reset clears all counters. Equivalent to creating a new sketch.
func (c *CountMinSketch) Reset() {
	for i := range c.counters {
		for j := range c.counters[i] {
			c.counters[i][j] = 0
		}
	}
}

// Width returns the number of columns (accuracy parameter).
func (c *CountMinSketch) Width() uint { return c.width }

// Depth returns the number of rows (confidence parameter).
func (c *CountMinSketch) Depth() uint { return c.depth }

// cmsHash computes a hash for key in row seed using FNV-1a mixed with the
// seed to produce independent hash functions per row.
//
// We simulate d independent hash functions via:
//
//	h_i(x) = FNV1a(x) XOR (seed * prime)
//
// This is a standard CMS implementation technique that avoids storing d
// separate hash function instances.
func cmsHash(key string, seed uint) uint64 {
	// FNV-1a base hash
	h := uint64(14695981039346656037)
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	// Mix in row seed to make each row's function independent.
	h ^= uint64(seed) * 2654435761 // Knuth's multiplicative hash constant
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}
