package metrics

import (
	"math"
	"sort"
	"sync/atomic"
)

type BucketSnapshot struct {
	UpperBound float64
	Count      uint64
}

type bucket struct {
	upperBound float64
	count      atomic.Uint64
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
	for i, bound := range sorted {
		buckets[i].upperBound = bound
	}
	buckets[len(sorted)].upperBound = math.Inf(1)

	return &Histogram{
		buckets: buckets,
	}
}

// Observe records a new value.
// Complexity: O(log N) for search + O(1) for atomic write.
func (m *Histogram) Observe(value float64) {
	idx := sort.Search(len(m.buckets), func(idx int) bool {
		return m.buckets[idx].upperBound >= value
	})

	m.buckets[idx].count.Add(1)
}

// Snapshot returns a snapshot of the histogram buckets.
func (m *Histogram) Snapshot() []BucketSnapshot {
	snapshot := make([]BucketSnapshot, len(m.buckets))
	for i := range m.buckets {
		snapshot[i] = BucketSnapshot{
			UpperBound: m.buckets[i].upperBound,
			Count:      m.buckets[i].count.Load(),
		}
	}
	return snapshot
}
