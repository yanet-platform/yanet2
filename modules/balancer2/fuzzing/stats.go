// Package fuzzing — latency statistics collector keyed by RPC/operation name.
//
// LatencyStats maintains per-operation samples for the current reporting
// window plus cumulative count/error totals that survive window resets. The
// runner is expected to call Record on every RPC and Report on each tick of
// the configured stats interval.
package fuzzing

import (
	"math"
	"sort"
	"time"
)

// LatencyStats collects per-operation latency samples and success/error
// counts. The zero value is not ready for use; construct with NewLatencyStats.
//
// LatencyStats is not safe for concurrent use. The fuzzing runner is
// single-threaded by design (see plan, Task 7), so callers do not need to
// synchronize Record and Report.
type LatencyStats struct {
	ops map[string]*opBucket
}

// opBucket holds the current-window samples plus cumulative counters for a
// single operation name.
type opBucket struct {
	samples         []time.Duration
	errors          uint64
	cumulativeCount uint64
	cumulativeErr   uint64
}

// OpReport is the per-operation snapshot produced by LatencyStats.Report for
// a single reporting window.
//
// Count/Errors describe the window that just closed; CumulativeCount and
// CumulativeErrors include every sample ever recorded for this operation,
// across all windows. Percentile fields use the nearest-rank algorithm
// documented on LatencyStats.Report.
type OpReport struct {
	Op               string
	Count            uint64
	Errors           uint64
	Avg              time.Duration
	P50              time.Duration
	P90              time.Duration
	P95              time.Duration
	P99              time.Duration
	Max              time.Duration
	CumulativeCount  uint64
	CumulativeErrors uint64
}

// NewLatencyStats returns an empty LatencyStats ready to receive samples.
func NewLatencyStats() *LatencyStats {
	return &LatencyStats{ops: map[string]*opBucket{}}
}

// Record appends a single observation for op. A non-nil err is counted as an
// error for both the current window and the cumulative total; the duration is
// still appended to the sample set so that percentiles reflect failed-call
// latency too.
func (m *LatencyStats) Record(op string, d time.Duration, err error) {
	bucket, ok := m.ops[op]
	if !ok {
		bucket = &opBucket{}
		m.ops[op] = bucket
	}
	bucket.samples = append(bucket.samples, d)
	bucket.cumulativeCount++
	if err != nil {
		bucket.errors++
		bucket.cumulativeErr++
	}
}

// Report produces a snapshot of every operation that has received at least
// one sample in the current window, then resets the per-window samples and
// error count. Cumulative totals are preserved across windows.
//
// Percentiles use the nearest-rank algorithm:
//
//	rank = ceil(percentile/100 * count), clamped to [1, count]
//
// The returned slice is sorted by operation name for stable output.
func (m *LatencyStats) Report() []OpReport {
	reports := make([]OpReport, 0, len(m.ops))
	for op, bucket := range m.ops {
		if len(bucket.samples) == 0 {
			continue
		}
		reports = append(reports, bucket.snapshot(op))
		bucket.samples = bucket.samples[:0]
		bucket.errors = 0
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Op < reports[j].Op
	})
	return reports
}

// snapshot builds an OpReport from the current window's samples. The caller
// is responsible for resetting bucket state after the snapshot is taken.
func (m *opBucket) snapshot(op string) OpReport {
	sorted := make([]time.Duration, len(m.samples))
	copy(sorted, m.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	count := uint64(len(sorted))
	avg := time.Duration(int64(total) / int64(count))

	return OpReport{
		Op:               op,
		Count:            count,
		Errors:           m.errors,
		Avg:              avg,
		P50:              nearestRank(sorted, 50),
		P90:              nearestRank(sorted, 90),
		P95:              nearestRank(sorted, 95),
		P99:              nearestRank(sorted, 99),
		Max:              sorted[len(sorted)-1],
		CumulativeCount:  m.cumulativeCount,
		CumulativeErrors: m.cumulativeErr,
	}
}

// nearestRank returns the nearest-rank percentile from a pre-sorted ascending
// slice. The rank is computed as ceil(percentile/100 * count) and clamped to
// [1, count]; the returned value is sorted[rank-1].
//
// Callers must pass a non-empty sorted slice; the function is undefined for
// an empty input.
func nearestRank(sorted []time.Duration, percentile float64) time.Duration {
	count := len(sorted)
	rank := int(math.Ceil(percentile / 100.0 * float64(count)))
	if rank < 1 {
		rank = 1
	}
	if rank > count {
		rank = count
	}
	return sorted[rank-1]
}
