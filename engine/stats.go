// Package engine — atomic operation counters for Phase 6.
//
// DBStats tracks reads, writes, and deletes using atomic Int64 fields.
// No mutex is needed: atomic.Load/Add are safe across goroutines by definition.
//
// Stats() on api.DB calls Snapshot() without acquiring db.mu — this is
// intentional. Mixing a lock with an atomic-only counter would be wrong:
// either everything is atomic, or everything is locked. Here everything is atomic.
package engine

import "sync/atomic"

// DBStats holds per-operation counters for the database.
// Safe for concurrent use without additional locking.
// Must not be copied after first use.
type DBStats struct {
	ReadsTotal   atomic.Int64 // Get + Find calls
	WritesTotal  atomic.Int64 // Insert + Update calls
	DeletesTotal atomic.Int64 // Delete calls
}

// NewDBStats returns a zeroed DBStats.
func NewDBStats() *DBStats { return &DBStats{} }

func (s *DBStats) IncReads()   { s.ReadsTotal.Add(1) }
func (s *DBStats) IncWrites()  { s.WritesTotal.Add(1) }
func (s *DBStats) IncDeletes() { s.DeletesTotal.Add(1) }

// StatsSnapshot is a point-in-time copy of all counters.
// Values are consistent with each other only approximately under concurrency —
// a writer may increment WritesTotal between the two Load calls. This is
// acceptable for observability; exact consistency would require a lock.
type StatsSnapshot struct {
	ReadsTotal   int64
	WritesTotal  int64
	DeletesTotal int64
}

// Snapshot returns a point-in-time copy of the counters.
// Does not acquire any lock.
func (s *DBStats) Snapshot() StatsSnapshot {
	return StatsSnapshot{
		ReadsTotal:   s.ReadsTotal.Load(),
		WritesTotal:  s.WritesTotal.Load(),
		DeletesTotal: s.DeletesTotal.Load(),
	}
}
