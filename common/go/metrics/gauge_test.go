package metrics

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGauge(t *testing.T) {
	t.Run("ZeroValue", func(t *testing.T) {
		var g Gauge
		assert.Equal(t, float64(0), g.Load(), "zero-value Gauge should be 0")
	})

	t.Run("StoreAndLoad", func(t *testing.T) {
		var g Gauge
		values := []float64{1.5, -3.14, 0, 1e10, -1e-10}
		for _, v := range values {
			g.Store(v)
			assert.Equal(t, v, g.Load(), "Load() should return stored value")
		}
	})

	t.Run("SpecialValues", func(t *testing.T) {
		var g Gauge

		g.Store(math.Inf(1))
		assert.True(t, math.IsInf(g.Load(), 1), "should store and load +Inf")

		g.Store(math.Inf(-1))
		assert.True(t, math.IsInf(g.Load(), -1), "should store and load -Inf")

		g.Store(math.NaN())
		assert.True(t, math.IsNaN(g.Load()), "should store and load NaN")

		g.Store(math.MaxFloat64)
		assert.Equal(t, math.MaxFloat64, g.Load(), "should store and load MaxFloat64")

		g.Store(math.SmallestNonzeroFloat64)
		assert.Equal(t, math.SmallestNonzeroFloat64, g.Load(), "should store and load SmallestNonzeroFloat64")
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
