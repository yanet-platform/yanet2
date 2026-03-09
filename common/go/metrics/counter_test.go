package metrics

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCounter(t *testing.T) {
	t.Run("ZeroValue", func(t *testing.T) {
		var c Counter
		assert.Equal(t, uint64(0), c.Load(), "zero-value Counter should be 0")
	})

	t.Run("Inc", func(t *testing.T) {
		var c Counter
		for i := uint64(1); i <= 5; i++ {
			got := c.Inc()
			assert.Equal(t, i, got, "Inc() should return incremented value")
			assert.Equal(t, i, c.Load(), "Load() should match incremented value")
		}
	})

	t.Run("Add", func(t *testing.T) {
		var c Counter
		got := c.Add(10)
		assert.Equal(t, uint64(10), got, "Add(10) should return 10")

		got = c.Add(5)
		assert.Equal(t, uint64(15), got, "Add(5) should return 15")

		assert.Equal(t, uint64(15), c.Load(), "Load() should return 15")
	})

	t.Run("AddZero", func(t *testing.T) {
		var c Counter
		c.Add(10)
		got := c.Add(0)
		assert.Equal(t, uint64(10), got, "Add(0) should not change value")
	})
}

func TestCounterConcurrent(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	n := 1000
	perGoroutine := uint64(10)

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Add(perGoroutine)
		}()
	}
	wg.Wait()

	want := uint64(n) * perGoroutine
	assert.Equal(t, want, c.Load(), "concurrent Add should produce correct total")
}
