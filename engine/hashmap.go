// Package engine implements core data structures for my-non-relational.
//
// # Fase 1b — Robin Hood Hashing
//
// HashMap uses open addressing with linear probing and Robin Hood displacement.
// On insert, if the incoming element's probe distance (DIB) exceeds that of the
// element currently occupying a slot, they swap — the "richer" element gives way
// to the "poorer" one. This minimizes variance in DIBs across the table.
//
// On delete, instead of leaving a tombstone, backward shift compaction closes
// the gap: each subsequent element with DIB > 0 slides one slot backward until
// an empty slot or an element in its ideal slot is reached.
//
// Why this matters:
//   - Tombstones accumulate over time and degrade lookup performance until the
//     next rehash. Backward shift maintains O(1) amortized lookup regardless of
//     delete density.
//   - Robin Hood displacement reduces worst-case probe chains, keeping p99
//     lookup latency low even at load factors near 0.9.
//   - Rust's std::collections::HashMap uses the same strategy.
//
// Trade-off: Delete is O(k) where k = length of the probe chain being shifted
// back, rather than O(1) for tombstone delete. In practice k is small (≤ 3-4
// at load 0.7).
//
// This file is NOT thread-safe; callers must synchronize externally.
package engine

import "hash/fnv"

const (
	initialCapacity = 16
	maxLoadFactor   = 0.7
)

// entry holds a key-value pair and its DIB (distance from initial bucket).
// DIB == -1 signals an empty slot.
type entry struct {
	key   string
	value map[string]any
	dib   int // distance from initial bucket; -1 = empty
}

func emptyEntry() entry { return entry{dib: -1} }

// HashMap is an open-addressing hash map with Robin Hood displacement
// and backward-shift deletion. No tombstones.
//
// Capacity is always a power of 2; rehash doubles it when load factor exceeds 0.7.
type HashMap struct {
	buckets  []entry
	count    int
	capacity int
}

// NewHashMap returns an initialized HashMap with capacity 16.
func NewHashMap() *HashMap {
	buckets := make([]entry, initialCapacity)
	for i := range buckets {
		buckets[i] = emptyEntry()
	}
	return &HashMap{
		buckets:  buckets,
		count:    0,
		capacity: initialCapacity,
	}
}

// hashKey computes the FNV-1a 64-bit hash of key.
func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key)) //nolint:errcheck
	return h.Sum64()
}

// fnv1a64 is a hand-rolled FNV-1a 64-bit hash — kept here as a reference
// implementation so the algorithm is visible without diving into the stdlib.
// Not used in production; hashKey() above delegates to hash/fnv.
//
// FNV-1a algorithm:
//
//	hash = offset_basis
//	for each byte b:
//	    hash = hash XOR b
//	    hash = hash × fnv_prime
//
// The XOR-then-multiply order (vs FNV-1's multiply-then-XOR) gives better
// avalanche for short keys — a small change in the input flips many output bits.
//
// Constants are defined by the FNV spec (https://www.isthe.com/chongo/tech/comp/fnv/):
//
//	offset basis = 14695981039346656037
//	prime        = 1099511628211
func fnv1a64(key string) uint64 { //nolint:unused
	const (
		offset uint64 = 14695981039346656037
		prime  uint64 = 1099511628211
	)
	h := offset
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= prime
	}
	return h
}

// idealSlot returns the bucket index where key would live with zero displacement.
func (m *HashMap) idealSlot(key string) int {
	return int(hashKey(key) % uint64(m.capacity))
}

// Set inserts or updates the value for key using Robin Hood displacement.
// If the load factor would exceed maxLoadFactor after insertion, rehash occurs first.
func (m *HashMap) Set(key string, value map[string]any) {
	if float64(m.count+1)/float64(m.capacity) > maxLoadFactor {
		m.rehash()
	}

	// cur is the element we're trying to place. Its dib starts at 0
	// (it hasn't been displaced yet) and grows with each probe step.
	cur := entry{key: key, value: value, dib: 0}
	pos := m.idealSlot(key)

	for i := 0; i < m.capacity; i++ {
		slot := (pos + i) % m.capacity
		e := &m.buckets[slot]

		if e.dib == -1 {
			// Empty slot: place cur here.
			cur.dib = i
			*e = cur
			m.count++
			logger.Debug("[hashmap] probe", "op", "insert", "key", key, "slot", slot, "dib", i)
			return
		}

		if e.key == cur.key {
			// Key already exists: update in-place, no count change.
			e.value = cur.value
			logger.Debug("[hashmap] probe", "op", "update", "key", key, "slot", slot)
			return
		}

		// Robin Hood: if cur has been displaced further than the element
		// occupying this slot, swap them. The "poorer" element takes the slot;
		// the "richer" displaced element continues looking.
		if e.dib < i {
			// cur takes this slot.
			cur.dib = i
			logger.Debug("[hashmap] rh_swap", "slot", slot, "incoming_dib", i, "resident_dib", e.dib, "displaced_key", e.key)
			cur, *e = *e, cur
			// cur is now the displaced element. Its ideal slot is:
			//   idealSlot = (slot - cur.dib + capacity) % capacity
			// We continue from the next slot; the displaced element's dib
			// will be incremented naturally as i increases.
			pos = (slot - cur.dib + m.capacity) % m.capacity
			i = cur.dib // loop will i++ to cur.dib+1 immediately
		}
	}
}

// Get returns the value associated with key and whether it was found.
// Robin Hood early exit: if the probed slot's DIB is less than our current
// probe distance, the key cannot exist further in the chain.
func (m *HashMap) Get(key string) (map[string]any, bool) {
	pos := m.idealSlot(key)
	for i := 0; i < m.capacity; i++ {
		slot := (pos + i) % m.capacity
		e := &m.buckets[slot]

		if e.dib == -1 || e.dib < i {
			// Empty slot or element with smaller DIB: our key is not here.
			logger.Debug("[hashmap] early_exit", "key", key, "slot", slot, "probe_steps", i)
			return nil, false
		}
		if e.key == key {
			logger.Debug("[hashmap] probe", "op", "get", "key", key, "slot", slot, "dib", e.dib)
			return e.value, true
		}
	}
	return nil, false
}

// Delete removes the key and performs backward-shift compaction.
// Returns true if the key was found and removed, false otherwise.
// No tombstones are left; subsequent elements slide back into the vacated slot.
func (m *HashMap) Delete(key string) bool {
	pos := m.idealSlot(key)
	for i := 0; i < m.capacity; i++ {
		slot := (pos + i) % m.capacity
		e := &m.buckets[slot]

		if e.dib == -1 || e.dib < i {
			return false // not found
		}
		if e.key != key {
			continue
		}

		// Found. Vacate this slot, then shift subsequent elements backward
		// until we hit an empty slot or an element already in its ideal slot.
		logger.Debug("[hashmap] delete_found", "key", key, "slot", slot, "probe_steps", i)
		m.buckets[slot] = emptyEntry()
		m.count--

		for {
			next := (slot + 1) % m.capacity
			if m.buckets[next].dib <= 0 {
				// dib == -1 (empty) or dib == 0 (already in ideal slot):
				// cannot shift back any further.
				break
			}
			logger.Debug("[hashmap] backward_shift", "from", next, "to", slot, "new_dib", m.buckets[next].dib-1, "key", m.buckets[next].key)
			m.buckets[slot] = m.buckets[next]
			m.buckets[slot].dib--
			m.buckets[next] = emptyEntry()
			slot = next
		}

		return true
	}
	return false
}

// Count returns the number of live (non-deleted) entries.
func (m *HashMap) Count() int {
	return m.count
}

// Capacity returns the current allocated slot count. Exposed for white-box tests.
func (m *HashMap) Capacity() int {
	return m.capacity
}

// All returns a slice of all live documents in undefined order.
// The returned slice contains the internal values directly — callers that
// need to mutate entries must copy them first.
func (m *HashMap) All() []map[string]any {
	out := make([]map[string]any, 0, m.count)
	for i := range m.buckets {
		if m.buckets[i].dib != -1 {
			out = append(out, m.buckets[i].value)
		}
	}
	return out
}

// rehash doubles the capacity and reinserts all occupied entries.
// Because there are no tombstones, every occupied entry is reinserted cleanly.
func (m *HashMap) rehash() {
	oldCap := m.capacity
	logger.Info("[hashmap] rehash", "old_cap", oldCap, "new_cap", oldCap*2, "count", m.count)
	old := m.buckets
	m.capacity *= 2
	m.buckets = make([]entry, m.capacity)
	for i := range m.buckets {
		m.buckets[i] = emptyEntry()
	}
	m.count = 0

	for _, e := range old {
		if e.dib != -1 {
			m.Set(e.key, e.value)
		}
	}
}
