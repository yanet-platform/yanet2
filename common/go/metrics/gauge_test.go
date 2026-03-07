package metrics

import (
	"math"
	"sync"
	"testing"
)

func TestGauge(t *testing.T) {
	t.Run("ZeroValue", func(t *testing.T) {
		var g Gauge
		if got := g.Load(); got != 0 {
			t.Errorf("zero-value Gauge.Load() = %v, want 0", got)
		}
	})

	t.Run("StoreAndLoad", func(t *testing.T) {
		var g Gauge
		values := []float64{1.5, -3.14, 0, 1e10, -1e-10}
		for _, v := range values {
			g.Store(v)
			if got := g.Load(); got != v {
				t.Errorf("Store(%v); Load() = %v", v, got)
			}
		}
	})

	t.Run("SpecialValues", func(t *testing.T) {
		var g Gauge

		g.Store(math.Inf(1))
		if got := g.Load(); !math.IsInf(got, 1) {
			t.Errorf("expected +Inf, got %v", got)
		}

		g.Store(math.Inf(-1))
		if got := g.Load(); !math.IsInf(got, -1) {
			t.Errorf("expected -Inf, got %v", got)
		}

		g.Store(math.NaN())
		if got := g.Load(); !math.IsNaN(got) {
			t.Errorf("expected NaN, got %v", got)
		}

		g.Store(math.MaxFloat64)
		if got := g.Load(); got != math.MaxFloat64 {
			t.Errorf("expected MaxFloat64, got %v", got)
		}

		g.Store(math.SmallestNonzeroFloat64)
		if got := g.Load(); got != math.SmallestNonzeroFloat64 {
			t.Errorf("expected SmallestNonzeroFloat64, got %v", got)
		}
	})
}

func TestGaugeConcurrent(t *testing.T) {
	var g Gauge
	var wg sync.WaitGroup
	n := 100

	for i := range n {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			g.Store(v)
			_ = g.Load()
		}(float64(i))
	}
	wg.Wait()

	// Just verify no race/panic - final value is non-deterministic
	_ = g.Load()
}
