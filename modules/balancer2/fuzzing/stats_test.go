package fuzzing

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recordSamples(t *testing.T, stats *LatencyStats, op string, samples []time.Duration) {
	t.Helper()
	for _, d := range samples {
		stats.Record(op, d, nil)
	}
}

func findReport(t *testing.T, reports []OpReport, op string) OpReport {
	t.Helper()
	for _, r := range reports {
		if r.Op == op {
			return r
		}
	}
	t.Fatalf("no report for op %q", op)
	return OpReport{}
}

// TestLatencyStats covers the canonical [1ms..5ms] sample set documented in
// the plan: avg, every required percentile, max, and error counters.
func TestLatencyStats(t *testing.T) {
	stats := NewLatencyStats()
	samples := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
	}
	recordSamples(t, stats, "UpdateVS", samples)

	reports := stats.Report()
	require.Len(t, reports, 1)
	report := reports[0]

	assert.Equal(t, "UpdateVS", report.Op)
	assert.Equal(t, uint64(5), report.Count)
	assert.Equal(t, uint64(0), report.Errors)
	assert.Equal(t, 3*time.Millisecond, report.Avg)
	assert.Equal(t, 3*time.Millisecond, report.P50)
	assert.Equal(t, 5*time.Millisecond, report.P90)
	assert.Equal(t, 5*time.Millisecond, report.P95)
	assert.Equal(t, 5*time.Millisecond, report.P99)
	assert.Equal(t, 5*time.Millisecond, report.Max)
	assert.Equal(t, uint64(5), report.CumulativeCount)
	assert.Equal(t, uint64(0), report.CumulativeErrors)
}

// TestLatencyStatsNearestRank pins the nearest-rank algorithm to its exact
// expected outputs for the [1ms..5ms] sample set so future refactors of the
// percentile implementation cannot regress silently.
func TestLatencyStatsNearestRank(t *testing.T) {
	stats := NewLatencyStats()
	for ms := 1; ms <= 5; ms++ {
		stats.Record("op", time.Duration(ms)*time.Millisecond, nil)
	}

	report := findReport(t, stats.Report(), "op")

	assert.Equal(t, 3*time.Millisecond, report.P50, "ceil(50/100*5)=3 → samples[2]=3ms")
	assert.Equal(t, 5*time.Millisecond, report.P90, "ceil(90/100*5)=5 → samples[4]=5ms")
	assert.Equal(t, 5*time.Millisecond, report.P95, "ceil(95/100*5)=5 → samples[4]=5ms")
	assert.Equal(t, 5*time.Millisecond, report.P99, "ceil(99/100*5)=5 → samples[4]=5ms")

	// Single-sample edge case: every percentile must collapse to that one
	// sample without panicking on rank=0.
	one := NewLatencyStats()
	one.Record("op", 7*time.Millisecond, nil)
	r := findReport(t, one.Report(), "op")
	assert.Equal(t, 7*time.Millisecond, r.P50)
	assert.Equal(t, 7*time.Millisecond, r.P99)
	assert.Equal(t, 7*time.Millisecond, r.Max)
}

// TestLatencyStatsResetWindow verifies that Report drains the per-window
// sample set and window-local error count but preserves cumulative totals.
func TestLatencyStatsResetWindow(t *testing.T) {
	stats := NewLatencyStats()
	for ms := 1; ms <= 5; ms++ {
		stats.Record("UpdateVS", time.Duration(ms)*time.Millisecond, nil)
	}
	stats.Record("UpdateVS", 6*time.Millisecond, errors.New("boom"))

	first := findReport(t, stats.Report(), "UpdateVS")
	assert.Equal(t, uint64(6), first.Count)
	assert.Equal(t, uint64(1), first.Errors)
	assert.Equal(t, uint64(6), first.CumulativeCount)
	assert.Equal(t, uint64(1), first.CumulativeErrors)

	// Second report immediately after the first: no new samples → no
	// report entry, but cumulative state is untouched.
	assert.Empty(t, stats.Report(), "empty window must produce no reports")

	// Record exactly one more sample and confirm cumulative count grows
	// from 6 → 7 while the window count is 1.
	stats.Record("UpdateVS", 10*time.Millisecond, nil)
	second := findReport(t, stats.Report(), "UpdateVS")
	assert.Equal(t, uint64(1), second.Count, "window must have been reset")
	assert.Equal(t, uint64(0), second.Errors, "window error count must reset")
	assert.Equal(t, uint64(7), second.CumulativeCount, "cumulative count must persist")
	assert.Equal(t, uint64(1), second.CumulativeErrors, "cumulative error count must persist")
}

// TestLatencyStatsMultipleOps confirms the collector keys samples by op name
// and that Report returns entries sorted by op for stable log output.
func TestLatencyStatsMultipleOps(t *testing.T) {
	stats := NewLatencyStats()
	stats.Record("UpdateVS", 2*time.Millisecond, nil)
	stats.Record("DeleteVS", 1*time.Millisecond, nil)
	stats.Record("GetState", 3*time.Millisecond, nil)

	reports := stats.Report()
	require.Len(t, reports, 3)
	assert.Equal(t, "DeleteVS", reports[0].Op)
	assert.Equal(t, "GetState", reports[1].Op)
	assert.Equal(t, "UpdateVS", reports[2].Op)
}

func TestFormatUpdateVS(t *testing.T) {
	s := FormatUpdateVS(42, "10.0.0.1:80/TCP", 7, 3*time.Millisecond)
	assert.Contains(t, s, "op=UpdateVS")
	assert.Contains(t, s, "num=42")
	assert.Contains(t, s, "vs=10.0.0.1:80/TCP")
	assert.Contains(t, s, "vs_count=7")
	assert.Contains(t, s, "dur=3ms")
}

func TestFormatDeleteVS(t *testing.T) {
	s := FormatDeleteVS(17, "10.0.0.1:80/TCP", 6, 2*time.Millisecond)
	assert.Contains(t, s, "op=DeleteVS")
	assert.Contains(t, s, "num=17")
	assert.Contains(t, s, "vs=10.0.0.1:80/TCP")
	assert.Contains(t, s, "vs_count=6")
}

func TestFormatUpdateReals(t *testing.T) {
	s := FormatUpdateReals(23, 3, 4, 5*time.Millisecond)
	assert.Contains(t, s, "op=UpdateReals")
	assert.Contains(t, s, "num=23")
	assert.Contains(t, s, "vs_count=3")
	assert.Contains(t, s, "real_updates=4")
}

func TestFormatGetState(t *testing.T) {
	s := FormatGetState(100, 5, 12, 8*time.Millisecond)
	assert.Contains(t, s, "op=GetState")
	assert.Contains(t, s, "num=100")
	assert.Contains(t, s, "vs_count=5")
	assert.Contains(t, s, "real_count=12")
}

func TestFormatInitCall(t *testing.T) {
	s := FormatInitCall("UpdateSessionsState", 0xC0FFEE, 15*time.Millisecond)
	assert.Contains(t, s, "op=Init")
	assert.Contains(t, s, "rpc=UpdateSessionsState")
	assert.Contains(t, s, "seed=12648430")
}

func TestFormatMismatch(t *testing.T) {
	s := FormatMismatch(7, []string{"extra_vs:1.2.3.4:80/TCP", "missing_real:5.6.7.8:9000"})
	assert.Contains(t, s, "op=Mismatch")
	assert.Contains(t, s, "num=7")
	assert.Contains(t, s, "extra_vs:1.2.3.4:80/TCP")
	assert.Contains(t, s, "missing_real:5.6.7.8:9000")
}

func TestFormatRPCFailure(t *testing.T) {
	s := FormatRPCFailure(11, "UpdateVS", errors.New("connection refused"))
	assert.Contains(t, s, "op=RPCFailure")
	assert.Contains(t, s, "num=11")
	assert.Contains(t, s, "rpc=UpdateVS")
	assert.Contains(t, s, "connection refused")
}
