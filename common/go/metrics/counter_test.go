package metrics

import (
	"sync"
	"testing"
)

func TestCounter(t *testing.T) {
	t.Run("ZeroValue", func(t *testing.T) {
		var c Counter
		if got := c.Load(); got != 0 {
			t.Errorf("zero-value Counter.Load() = %v, want 0", got)
		}
	})

	t.Run("Inc", func(t *testing.T) {
		var c Counter
		for i := uint64(1); i <= 5; i++ {
			got := c.Inc()
			if got != i {
				t.Errorf("Inc() = %v, want %v", got, i)
			}
			if c.Load() != i {
				t.Errorf("Load() = %v, want %v", c.Load(), i)
			}
		}
	})

	t.Run("Add", func(t *testing.T) {
		var c Counter
		got := c.Add(10)
		if got != 10 {
			t.Errorf("Add(10) = %v, want 10", got)
		}

		got = c.Add(5)
		if got != 15 {
			t.Errorf("Add(5) = %v, want 15", got)
		}

		if c.Load() != 15 {
			t.Errorf("Load() = %v, want 15", c.Load())
		}
	})

	t.Run("AddZero", func(t *testing.T) {
		var c Counter
		c.Add(10)
		got := c.Add(0)
		if got != 10 {
			t.Errorf("Add(0) = %v, want 10", got)
		}
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
	if got := c.Load(); got != want {
		t.Errorf("concurrent Add: got %v, want %v", got, want)
	}
}
