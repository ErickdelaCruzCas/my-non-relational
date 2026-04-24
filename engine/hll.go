// Package engine — Fase 8: HyperLogLog
//
// HyperLogLog (HLL) estimates the cardinality (distinct count) of a stream
// using O(m) bytes of memory, where m is the number of registers (here m=1024).
// For 1 million distinct values, the exact count needs ~50 MB (hash set);
// HLL uses 1 KB with ~2% error.
//
// # How it works
//
// For each element:
//  1. Hash it to a 64-bit value.
//  2. Use the first log2(m) bits to select a register (bucket).
//  3. Count the number of leading zeros in the remaining bits.
//     This is the "rank" — rare in a uniform stream, hence a signal of large N.
//  4. Update register[bucket] = max(register[bucket], rank + 1).
//
// Estimate:
//
//	E = α_m * m² * harmonic_mean(2^(-register[i]))
//
// The harmonic mean of 2^(-register[i]) converges to 1/N as N grows.
// α_m is a bias correction constant.
//
// # Use in this DB
//
// In Stats() and Explain(), HyperLogLog estimates the cardinality of each
// indexed field. Example: "city has ~50,000 distinct values". This feeds the
// query planner's EstimatedDocs calculation for INDEX_SCAN vs FULL_SCAN decisions.
//
// Trade-off: exact cardinality (hash set) uses O(N) memory and is always correct.
// HLL uses O(m) ≈ 1 KB at 2% error. For large N (> 10,000), this is the right
// trade-off for a background stats system.
//
// Error: ~1.04/sqrt(m) ≈ 3.25% for m=1024. With bias correction for small N,
// practical error is ~2% for N > 10*m.
package engine

import (
	"math"
	"math/bits"
)

const (
	hllP        = 10             // log2(m); m = 1024 registers
	hllM        = 1 << hllP     // 1024 registers
	hllMaxValue = hllM * 64 / 8 // max register value in theory
)

// alphaM is the bias correction constant for m=1024 registers.
// Derived from α = 0.7213 / (1 + 1.079/m) for m ≥ 128.
var hllAlpha = 0.7213 / (1.0 + 1.079/float64(hllM))

// HyperLogLog estimates cardinality using 1024 8-bit registers (~1 KB).
// Each register stores the maximum "leading zeros + 1" seen for its bucket.
type HyperLogLog struct {
	regs [hllM]uint8
}

// NewHyperLogLog returns a zeroed HyperLogLog ready to accept elements.
func NewHyperLogLog() *HyperLogLog {
	return &HyperLogLog{}
}

// Add incorporates value into the cardinality estimate.
// Internally: hash → select register → update with leading-zero count.
func (h *HyperLogLog) Add(value string) {
	hash := hllHash(value)

	// Use the top hllP bits to select the register (0..1023).
	reg := hash >> (64 - hllP)

	// Count leading zeros in the remaining 64-hllP bits (the "rank").
	// We look at the lower 64-hllP bits; prepend a 1-bit as a sentinel.
	tail := (hash << hllP) | (1<<hllP - 1) // ensure at least 1 bit set
	rank := uint8(bits.LeadingZeros64(tail)) + 1

	updated := rank > h.regs[reg]
	if updated {
		h.regs[reg] = rank
	}
	logger.Debug("[hll] add", "value", value, "reg", reg, "rank", rank, "updated", updated)
}

// Estimate returns the estimated number of distinct elements added so far.
//
// Three zones handle edge cases:
//  1. Small range  (E ≤ 2.5*m):  use LinearCounting if there are empty registers.
//  2. Normal range (2.5*m < E ≤ 2^32/30): return raw HLL estimate.
//  3. Large range  (E > 2^32/30): apply large-range correction (hash collisions).
func (h *HyperLogLog) Estimate() uint64 {
	sum := 0.0
	zeros := 0
	for _, r := range h.regs {
		sum += math.Pow(2, -float64(r))
		if r == 0 {
			zeros++
		}
	}

	// Raw HLL estimate.
	E := hllAlpha * float64(hllM) * float64(hllM) / sum
	raw := E
	correction := "none"

	// Small-range correction: use LinearCounting when many registers are zero.
	if E <= 2.5*float64(hllM) && zeros > 0 {
		E = float64(hllM) * math.Log(float64(hllM)/float64(zeros))
		correction = "linear_counting"
	}

	// Large-range correction: 2^32 wrap-around.
	const twoTo32 = float64(1 << 32)
	if E > twoTo32/30 {
		E = -twoTo32 * math.Log(1-E/twoTo32)
		correction = "large_range"
	}

	logger.Info("[hll] estimate", "raw", uint64(raw), "correction", correction, "result", uint64(E), "zero_regs", zeros)
	return uint64(E)
}

// Merge combines another HyperLogLog into h by taking the element-wise maximum
// of registers. After Merge, h estimates the cardinality of the union of both
// sets. This is used when merging stats from multiple index shards.
func (h *HyperLogLog) Merge(other *HyperLogLog) {
	for i := range h.regs {
		if other.regs[i] > h.regs[i] {
			h.regs[i] = other.regs[i]
		}
	}
}

// Reset clears all registers, as if no elements had been added.
func (h *HyperLogLog) Reset() {
	for i := range h.regs {
		h.regs[i] = 0
	}
}

// hllHash computes a 64-bit hash of value using FNV-1a.
// FNV-1a is fast and has good avalanche properties for short strings.
func hllHash(value string) uint64 {
	h := uint64(14695981039346656037) // FNV offset basis
	for i := 0; i < len(value); i++ {
		h ^= uint64(value[i])
		h *= 1099511628211 // FNV prime
	}
	return h
}
