package metrics

import (
	"math"
	"sort"
	"sync/atomic"

	"github.com/yanet-platform/yanet2/common/commonpb"
)

type Histogram struct {
	bounds []float64

	// buckets holds the counters.
	// len(buckets) == len(bounds) + 1.
	// The last bucket is for values > the last bound (+Inf).
	buckets []atomic.Uint64

	// count tracks the total number of observations.
	count atomic.Uint64
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
	h.count.Add(1)

	idx := sort.SearchFloat64s(h.bounds, value)

	h.buckets[idx].Add(1)
}

// ToProtoHistogram converts the concurrent histogram into the Protobuf format.
// This calculates the CUMULATIVE distribution (standard for visualizing latencies).
func (h *Histogram) ToProtoHistogram() *commonpb.Histogram {
	resp := &commonpb.Histogram{
		TotalCount: h.count.Load(),
		Buckets:    make([]*commonpb.Bucket, 0, len(h.bounds)+1),
	}

	// We read the atomic values one by one and accumulate sum.
	// Note: Slight inconsistencies might occur between buckets because updates
	// are happening while we read, but this is acceptable for telemetry.
	var accumulatedCount uint64

	for i, bound := range h.bounds {
		bucketCount := h.buckets[i].Load()
		accumulatedCount += bucketCount

		resp.Buckets = append(resp.Buckets, &commonpb.Bucket{
			UpperBound: bound,
			Count:      accumulatedCount,
		})
	}

	infCount := h.buckets[len(h.bounds)].Load()
	accumulatedCount += infCount

	resp.Buckets = append(resp.Buckets, &commonpb.Bucket{
		UpperBound: math.Inf(1),
		Count:      accumulatedCount,
	})

	return resp
}

func (h *Histogram) ToProto() *commonpb.MetricValue {
	return &commonpb.MetricValue{Value: &commonpb.MetricValue_Histogram{Histogram: h.ToProtoHistogram()}}
}
