package metrics

import (
	"slices"
	"sync"
	"testing"
)

func TestNewHistogram(t *testing.T) {
	t.Run("SortsBounds", func(t *testing.T) {
		h := NewHistogram([]float64{100, 10, 50, 1})
		want := []float64{1, 10, 50, 100}
		if !slices.Equal(h.Bounds, want) {
			t.Errorf("Bounds = %v, want %v", h.Bounds, want)
		}
	})

	t.Run("BucketCount", func(t *testing.T) {
		h := NewHistogram([]float64{1, 5, 10})
		if got := len(h.Buckets); got != 4 {
			t.Errorf("len(Buckets) = %v, want 4", got)
		}
	})

	t.Run("EmptyBounds", func(t *testing.T) {
		h := NewHistogram([]float64{})
		if len(h.Bounds) != 0 {
			t.Errorf("len(Bounds) = %v, want 0", len(h.Bounds))
		}
		if len(h.Buckets) != 1 {
			t.Errorf("len(Buckets) = %v, want 1 (inf bucket)", len(h.Buckets))
		}
	})

	t.Run("DoesNotModifyInput", func(t *testing.T) {
		input := []float64{3, 1, 2}
		original := slices.Clone(input)
		_ = NewHistogram(input)
		if !slices.Equal(input, original) {
			t.Errorf("input modified: got %v, want %v", input, original)
		}
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
			before := h.Buckets[tc.bucket].Load()
			h.Observe(tc.value)
			after := h.Buckets[tc.bucket].Load()
			if after != before+1 {
				t.Errorf("Observe(%v): bucket[%d] = %v, want %v", tc.value, tc.bucket, after, before+1)
			}
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
		before := h.Buckets[tc.bucket].Load()
		h.Observe(tc.value)
		after := h.Buckets[tc.bucket].Load()
		if after != before+1 {
			t.Errorf("Observe(%v): bucket[%d] = %v, want %v", tc.value, tc.bucket, after, before+1)
		}
	}
}

func TestHistogramObserveEmptyBounds(t *testing.T) {
	h := NewHistogram([]float64{})

	// All values go to the single inf bucket
	for _, v := range []float64{-100, 0, 100} {
		before := h.Buckets[0].Load()
		h.Observe(v)
		if h.Buckets[0].Load() != before+1 {
			t.Errorf("Observe(%v) did not increment inf bucket", v)
		}
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

	var total uint64
	for i := range h.Buckets {
		total += h.Buckets[i].Load()
	}
	if total != uint64(n) {
		t.Errorf("total observations = %v, want %v", total, n)
	}
}
