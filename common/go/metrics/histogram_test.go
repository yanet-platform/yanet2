package metrics

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHistogram(t *testing.T) {
	t.Run("SortsBounds", func(t *testing.T) {
		h := NewHistogram([]float64{100, 10, 50, 1})
		snapshot := h.Snapshot()
		// Verify bounds are sorted (excluding +Inf)
		bounds := make([]float64, 0, len(snapshot)-1)
		for i := 0; i < len(snapshot)-1; i++ {
			bounds = append(bounds, snapshot[i].UpperBound)
		}
		want := []float64{1, 10, 50, 100}
		assert.Equal(t, want, bounds, "bounds should be sorted")
	})

	t.Run("BucketCount", func(t *testing.T) {
		h := NewHistogram([]float64{1, 5, 10})
		snapshot := h.Snapshot()
		assert.Equal(t, 4, len(snapshot), "should have 4 buckets (3 bounds + inf)")
	})

	t.Run("EmptyBounds", func(t *testing.T) {
		h := NewHistogram([]float64{})
		snapshot := h.Snapshot()
		assert.Len(t, snapshot, 1, "should have 1 inf bucket")
		assert.True(t, math.IsInf(snapshot[0].UpperBound, 1), "single bucket should be +Inf")
	})

	t.Run("DoesNotModifyInput", func(t *testing.T) {
		input := []float64{3, 1, 2}
		original := slices.Clone(input)
		_ = NewHistogram(input)
		assert.Equal(t, original, input, "input should not be modified")
	})
}

func TestHistogramObserve(t *testing.T) {
	// Bounds: [1, 5, 10]
	// Buckets: [0]: <=1, [1]: (1,5], [2]: (5,10], [3]: >10 (inf)
	h := NewHistogram([]float64{1, 5, 10})

	tests := []struct {
		name   string
		value  float64
		bucket int
	}{
		{"BelowMin", 0.5, 0},
		{"ExactMin", 1, 0},
		{"BetweenFirstSecond", 3, 1},
		{"ExactMiddle", 5, 1},
		{"BetweenMiddleLast", 7, 2},
		{"ExactMax", 10, 2},
		{"AboveMax", 100, 3},
		{"Negative", -5, 0},
		{"Zero", 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := h.Snapshot()
			before := snapshot[tc.bucket].Count
			h.Observe(tc.value)
			snapshot = h.Snapshot()
			after := snapshot[tc.bucket].Count
			assert.Equal(t, before+1, after, "bucket should be incremented by 1")
		})
	}
}

func TestHistogramObserveNegativeBounds(t *testing.T) {
	// Bounds: [-10, -5, 0, 5] (sorted)
	// Buckets: [0]: <=-10, [1]: (-10,-5], [2]: (-5,0], [3]: (0,5], [4]: >5
	h := NewHistogram([]float64{0, -5, 5, -10})

	tests := []struct {
		value  float64
		bucket int
	}{
		{-15, 0},
		{-10, 0},
		{-7, 1},
		{-5, 1},
		{-1, 2},
		{0, 2},
		{3, 3},
		{5, 3},
		{100, 4},
	}

	for _, tc := range tests {
		name := fmt.Sprintf("value=%v", tc.value)
		t.Run(name, func(t *testing.T) {
			snapshot := h.Snapshot()
			before := snapshot[tc.bucket].Count
			h.Observe(tc.value)
			snapshot = h.Snapshot()
			after := snapshot[tc.bucket].Count
			assert.Equal(t, before+1, after, "bucket should be incremented by 1 for value %v", tc.value)
		})
	}
}

func TestHistogramObserveEmptyBounds(t *testing.T) {
	h := NewHistogram([]float64{})

	// All values go to the single inf bucket
	for _, v := range []float64{-100, 0, 100} {
		name := fmt.Sprintf("value=%v", v)
		t.Run(name, func(t *testing.T) {
			snapshot := h.Snapshot()
			before := snapshot[0].Count
			h.Observe(v)
			snapshot = h.Snapshot()
			assert.Equal(t, before+1, snapshot[0].Count, "inf bucket should be incremented for value %v", v)
		})
	}
}

func TestHistogramConcurrent(t *testing.T) {
	h := NewHistogram([]float64{10, 50, 100})
	var wg sync.WaitGroup
	n := 1000

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Observe(25) // goes to bucket[1]
		}()
	}
	wg.Wait()

	snapshot := h.Snapshot()
	var total uint64
	for _, bucket := range snapshot {
		total += bucket.Count
	}
	assert.Equal(t, uint64(n), total, "total observations should match")
}

func TestHistogramSnapshot(t *testing.T) {
	t.Run("EmptyHistogram", func(t *testing.T) {
		h := NewHistogram([]float64{10, 50, 100})
		snapshot := h.Snapshot()

		require.Len(t, snapshot, 4, "should have 4 buckets")

		// Verify all counts are zero
		for i, bucket := range snapshot {
			assert.Equal(t, uint64(0), bucket.Count, "bucket %d should have zero count", i)
		}

		// Verify bounds
		assert.Equal(t, 10.0, snapshot[0].UpperBound, "first bound should be 10")
		assert.Equal(t, 50.0, snapshot[1].UpperBound, "second bound should be 50")
		assert.Equal(t, 100.0, snapshot[2].UpperBound, "third bound should be 100")
		assert.True(t, math.IsInf(snapshot[3].UpperBound, 1), "last bound should be +Inf")
	})

	t.Run("WithObservations", func(t *testing.T) {
		h := NewHistogram([]float64{10, 50, 100})
		h.Observe(5)   // bucket 0
		h.Observe(25)  // bucket 1
		h.Observe(75)  // bucket 2
		h.Observe(200) // bucket 3

		snapshot := h.Snapshot()

		require.Len(t, snapshot, 4, "should have 4 buckets")

		// Verify counts
		assert.Equal(t, uint64(1), snapshot[0].Count, "bucket 0 should have 1 observation")
		assert.Equal(t, uint64(1), snapshot[1].Count, "bucket 1 should have 1 observation")
		assert.Equal(t, uint64(1), snapshot[2].Count, "bucket 2 should have 1 observation")
		assert.Equal(t, uint64(1), snapshot[3].Count, "bucket 3 should have 1 observation")

		// Verify bounds
		assert.Equal(t, 10.0, snapshot[0].UpperBound, "bucket 0 upper bound")
		assert.Equal(t, 50.0, snapshot[1].UpperBound, "bucket 1 upper bound")
		assert.Equal(t, 100.0, snapshot[2].UpperBound, "bucket 2 upper bound")
		assert.True(t, math.IsInf(snapshot[3].UpperBound, 1), "bucket 3 should be +Inf")
	})

	t.Run("SnapshotIsImmutable", func(t *testing.T) {
		h := NewHistogram([]float64{10})
		h.Observe(5)

		snapshot1 := h.Snapshot()
		assert.Equal(t, uint64(1), snapshot1[0].Count, "initial snapshot should have 1 observation")

		// Add more observations
		h.Observe(5)
		h.Observe(5)

		// Original snapshot should be unchanged
		assert.Equal(t, uint64(1), snapshot1[0].Count, "original snapshot should be unchanged")

		// New snapshot should reflect updates
		snapshot2 := h.Snapshot()
		assert.Equal(t, uint64(3), snapshot2[0].Count, "new snapshot should have 3 observations")
	})

	t.Run("EmptyBoundsHistogram", func(t *testing.T) {
		h := NewHistogram([]float64{})
		h.Observe(100)
		h.Observe(-100)

		snapshot := h.Snapshot()

		require.Len(t, snapshot, 1, "should have 1 bucket")
		assert.Equal(t, uint64(2), snapshot[0].Count, "inf bucket should have 2 observations")
		assert.True(t, math.IsInf(snapshot[0].UpperBound, 1), "should be +Inf")
	})
}
