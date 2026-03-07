package metrics

import (
	"sort"
	"sync/atomic"
)

type Histogram struct {
	Bounds []float64

	// Buckets holds the counters.
	// len(Buckets) == len(bounds) + 1.
	// The last bucket is for values > the last bound (+Inf).
	Buckets []atomic.Uint64
}

func NewHistogram(bounds []float64) *Histogram {
	sorted := make([]float64, len(bounds))
	copy(sorted, bounds)
	sort.Float64s(sorted)

	return &Histogram{
		Bounds: sorted,
		// we need 1 extra bucket for the "infinite" bucket (values > max bound)
		Buckets: make([]atomic.Uint64, len(sorted)+1),
	}
}

// Observe records a new value.
// Complexity: O(log N) for search + O(1) for atomic write.
func (h *Histogram) Observe(value float64) {
	idx := sort.SearchFloat64s(h.Bounds, value)

	h.Buckets[idx].Add(1)
}
