// Package engine — Fase 3: Bloom Filter
//
// A Bloom Filter answers "definitely not in set" or "probably in set" in O(1)
// time and O(m) bits of space, where m is the bit array size.
//
// Use case in this DB: before doing a binary search in the primary index for
// Get(id), check the Bloom Filter. If it says "definitely not", skip the disk
// read entirely. At 10 bits/element and 7 hash functions, the false positive
// rate is ~0.8%.
//
// Pipeline after adding Bloom Filter to Get(id):
//
//	id → BloomFilter.MayContain(id)
//	        ├── false → return ErrNotFound immediately  O(1)
//	        └── true  → binary_search → ReadAt → doc
//
// Trade-off:
//   - Memory: 10 bits × N IDs extra RAM. For 1M documents: ~1.2 MB.
//   - False positives cause unnecessary binary searches (~0.8% of lookups).
//   - False negatives are impossible by construction.
//   - Bloom Filters cannot support Delete (deleting would flip shared bits).
//     In Phase 9 (compaction), rebuild the filter from scratch.
//
// Educational note: this is the first probabilistic structure in the project.
// Unlike the hash map (exact) and sorted slice (exact), this structure trades
// correctness for speed. This trade-off is fundamental to understanding
// production databases like RocksDB and Cassandra, which keep a Bloom Filter
// per SSTable level.
package engine

import (
	"math"
)

// BloomFilter is a space-efficient probabilistic set membership tester.
// It uses two independent hash families (FNV-1a and DJB2) to simulate k
// hash functions via double hashing: h_i(x) = h1(x) + i*h2(x).
type BloomFilter struct {
	bits    []uint64 // bit array stored as uint64 words
	m       uint     // total number of bits
	k       uint     // number of hash functions
	numBits uint     // number of 64-bit words
}

// NewBloomFilter creates a Bloom Filter sized for n expected elements at
// the given false positive rate (0 < fpRate < 1).
//
// Optimal parameters:
//
//	m = -n * ln(fpRate) / (ln(2)^2)   bits
//	k = (m/n) * ln(2)                  hash functions
//
// Typical values: n=100_000, fpRate=0.01 → m≈958_505 bits (~117 KB), k=7.
func NewBloomFilter(n uint, fpRate float64) *BloomFilter {
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}
	if n == 0 {
		n = 1
	}

	// Number of bits.
	m := uint(math.Ceil(-float64(n) * math.Log(fpRate) / (math.Log(2) * math.Log(2))))
	// Round up to next multiple of 64.
	if m == 0 {
		m = 64
	}
	numWords := (m + 63) / 64
	m = numWords * 64 // actual bit count (rounded up)

	// Number of hash functions.
	k := uint(math.Round(float64(m) / float64(n) * math.Log(2)))
	if k < 1 {
		k = 1
	}

	logger.Info("[bloom] created", "n", n, "fp_rate", fpRate, "bits_m", m, "hash_fns_k", k)
	return &BloomFilter{
		bits:    make([]uint64, numWords),
		m:       m,
		k:       k,
		numBits: numWords,
	}
}

// Add inserts key into the filter by setting k bits.
func (bf *BloomFilter) Add(key string) {
	h1, h2 := bloomHashes(key)
	for i := uint(0); i < bf.k; i++ {
		bit := (h1 + uint64(i)*h2) % uint64(bf.m)
		bf.bits[bit/64] |= 1 << (bit % 64)
	}
	logger.Debug("[bloom] add", "key", key, "hash_fns_k", bf.k)
}

// MayContain returns false if key is definitely NOT in the set.
// Returns true if key is probably in the set (false positive possible).
func (bf *BloomFilter) MayContain(key string) bool {
	h1, h2 := bloomHashes(key)
	for i := uint(0); i < bf.k; i++ {
		bit := (h1 + uint64(i)*h2) % uint64(bf.m)
		if bf.bits[bit/64]&(1<<(bit%64)) == 0 {
			logger.Info("[bloom] may_contain", "key", key, "result", false)
			return false // definitely not present
		}
	}
	logger.Info("[bloom] may_contain", "key", key, "result", true)
	return true // probably present
}

// EstimatedFPRate returns the current empirical false positive rate based on
// how many bits are set. Useful for monitoring filter saturation.
//
//	fp ≈ (1 - e^(-k*n/m))^k
//
// In practice: if this exceeds 2×target rate, rebuild the filter.
func (bf *BloomFilter) EstimatedFPRate(n uint) float64 {
	if bf.m == 0 || n == 0 {
		return 0
	}
	ratio := float64(bf.k) * float64(n) / float64(bf.m)
	return math.Pow(1-math.Exp(-ratio), float64(bf.k))
}

// BitCount returns how many bits are currently set. Useful for diagnostics.
func (bf *BloomFilter) BitCount() int {
	total := 0
	for _, word := range bf.bits {
		total += popcount(word)
	}
	return total
}

// M returns the total bit capacity.
func (bf *BloomFilter) M() uint { return bf.m }

// K returns the number of hash functions.
func (bf *BloomFilter) K() uint { return bf.k }

// bloomHashes computes two independent 64-bit hashes of key using
// FNV-1a and a DJB2 variant. These serve as the two basis functions for
// double hashing: h_i(x) = h1(x) + i*h2(x).
//
// Double hashing with two independent hash functions is equivalent to using
// k independent hash functions when k is small relative to m (Kirsch-Mitzenmacher).
func bloomHashes(key string) (h1, h2 uint64) {
	// FNV-1a 64-bit
	h1 = 14695981039346656037 // FNV offset basis
	for i := 0; i < len(key); i++ {
		h1 ^= uint64(key[i])
		h1 *= 1099511628211 // FNV prime
	}

	// DJB2 variant (XOR version), seeded differently from FNV
	h2 = 5381
	for i := 0; i < len(key); i++ {
		h2 = ((h2 << 5) + h2) ^ uint64(key[i])
	}
	// Ensure h2 is odd to cover all bit positions (required for double hashing)
	h2 |= 1

	return h1, h2
}

// popcount counts the number of set bits in a uint64 word (Hamming weight).
// Uses the standard bit-twiddling approach.
func popcount(x uint64) int {
	x -= (x >> 1) & 0x5555555555555555
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}
