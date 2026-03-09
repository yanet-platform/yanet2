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

type Histogram struct {
	bounds []float64

	// Buckets holds the counters.
	// len(Buckets) == len(bounds) + 1.
	// The last bucket is for values > the last bound (+Inf).
	buckets []atomic.Uint64
}

func NewHistogram(bounds []float64) *Histogram {
	sorted := make([]float64, len(bounds))
	copy(sorted, bounds)
	sort.Float64s(sorted)

	return &Histogram{
		bounds: sorted,
		// we need 1 extra bucket for the "infinite" bucket (values > max bound)
		buckets: make([]atomic.Uint64, len(sorted)+1),
	}
}

// Observe records a new value.
// Complexity: O(log N) for search + O(1) for atomic write.
func (m *Histogram) Observe(value float64) {
	idx := sort.SearchFloat64s(m.bounds, value)

	m.buckets[idx].Add(1)
}

// Snapshot returns a snapshot of the histogram buckets.
func (m *Histogram) Snapshot() []BucketSnapshot {
	snapshot := make([]BucketSnapshot, len(m.buckets))
	for i := range m.bounds {
		snapshot[i] = BucketSnapshot{
			UpperBound: m.bounds[i],
			Count:      m.buckets[i].Load(),
		}
	}
	snapshot[len(m.bounds)] = BucketSnapshot{
		UpperBound: math.Inf(1),
		Count:      m.buckets[len(m.bounds)].Load(),
	}
	return snapshot
}
