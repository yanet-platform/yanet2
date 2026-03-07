package metrics

import (
	"sort"
	"sync/atomic"
)

type Histogram struct {
	bounds []float64

	// buckets holds the counters.
	// len(buckets) == len(bounds) + 1.
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
func (h *Histogram) Observe(value float64) {
	idx := sort.SearchFloat64s(h.bounds, value)

	h.buckets[idx].Add(1)
}

func (h *Histogram) Buckets() []atomic.Uint64 {
	return h.buckets
}
