package metrics

import (
	"math"
	"sort"
	"sync/atomic"
)

// BucketSnapshot is a point-in-time read of one histogram bucket.
type BucketSnapshot struct {
	UpperBound float64
	Count      uint64
}

// bucket is one histogram bucket: an upper bound and an observation count.
type bucket struct {
	upperBound float64

	count atomic.Uint64
}

// newBucket returns a fresh bucket accepting values up to upperBound.
func newBucket(upperBound float64) bucket {
	return bucket{upperBound: upperBound}
}

// Accepts reports whether value falls within this bucket's upper bound.
func (m *bucket) Accepts(value float64) bool {
	return m.upperBound >= value
}

// Record counts one observation in this bucket.
func (m *bucket) Record() {
	m.count.Add(1)
}

// Snapshot returns a point-in-time read of this single bucket.
func (m *bucket) Snapshot() BucketSnapshot {
	return BucketSnapshot{
		UpperBound: m.upperBound,
		Count:      m.count.Load(),
	}
}

type Histogram struct {
	// Must be sorted by upperBound.
	buckets []bucket
}

func NewHistogram(bounds []float64) *Histogram {
	sorted := make([]float64, len(bounds))
	copy(sorted, bounds)
	sort.Float64s(sorted)

	buckets := make([]bucket, len(sorted)+1)
	for idx, bound := range sorted {
		buckets[idx] = newBucket(bound)
	}
	buckets[len(sorted)] = newBucket(math.Inf(1))

	return &Histogram{
		buckets: buckets,
	}
}

// Observe records a new value.
// Complexity: O(log N) for search + O(1) for atomic write.
func (m *Histogram) Observe(value float64) {
	idx := sort.Search(len(m.buckets), func(idx int) bool {
		return m.buckets[idx].Accepts(value)
	})

	m.buckets[idx].Record()
}

// Snapshot returns a snapshot of all histogram buckets.
func (m *Histogram) Snapshot() []BucketSnapshot {
	snapshot := make([]BucketSnapshot, len(m.buckets))
	for idx := range m.buckets {
		snapshot[idx] = m.buckets[idx].Snapshot()
	}
	return snapshot
}
