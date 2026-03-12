package metrics

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricMapGetOrCreate(t *testing.T) {
	t.Run("CreatesNew", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test", Labels: Labels{"a": "1"}}

		var calls int
		c := m.GetOrCreate(id, func() *Counter { calls++; return &Counter{} })

		assert.Equal(t, 1, calls, "create should be called once")
		require.NotNil(t, c, "GetOrCreate should not return nil")
	})

	t.Run("ReturnsExisting", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test"}

		c1 := m.GetOrCreate(id, func() *Counter { return &Counter{} })
		c1.Inc()

		var calls int
		c2 := m.GetOrCreate(id, func() *Counter { calls++; return &Counter{} })

		assert.Equal(t, 0, calls, "create should not be called for existing metric")
		assert.Same(t, c1, c2, "should return same pointer for same ID")
		assert.Equal(t, uint64(1), c2.Load(), "existing metric value should be preserved")
	})

	t.Run("DifferentIDs", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id1 := MetricID{Name: "metric1"}
		id2 := MetricID{Name: "metric2"}

		c1 := m.GetOrCreate(id1, func() *Counter { return &Counter{} })
		c2 := m.GetOrCreate(id2, func() *Counter { return &Counter{} })

		c1.Add(10)
		c2.Add(20)

		assert.Equal(t, uint64(10), c1.Load(), "metric1 should have correct value")
		assert.Equal(t, uint64(20), c2.Load(), "metric2 should have correct value")
	})
}

func TestMetricMapLabelsAreASet(t *testing.T) {
	m := NewMetricMap[*Counter]()

	id1 := MetricID{Name: "test", Labels: Labels{"a": "1", "b": "2"}}
	id2 := MetricID{Name: "test", Labels: Labels{"b": "2", "a": "1"}}

	c1 := m.GetOrCreate(id1, func() *Counter { return &Counter{} })
	c2 := m.GetOrCreate(id2, func() *Counter { return &Counter{} })

	assert.Same(t, c1, c2, "same label set should resolve to the same metric")

	c1.Inc()
	assert.Equal(t, uint64(1), c2.Load(), "should observe updates through either reference")
}

func TestMetricMapMetrics(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		refs := m.Metrics()
		assert.Empty(t, refs, "Metrics() on empty map should return empty slice")
	})

	t.Run("ReturnsAll", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		for i := range 5 {
			id := MetricID{Name: fmt.Sprintf("metric%d", i)}
			m.GetOrCreate(id, func() *Counter { return &Counter{} })
		}

		refs := m.Metrics()
		assert.Len(t, refs, 5, "Metrics() should return all metrics")
	})

	t.Run("LiveReferences", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test"}
		c := m.GetOrCreate(id, func() *Counter { return &Counter{} })

		refs := m.Metrics()
		require.Len(t, refs, 1, "should have 1 metric")

		// Modify via original pointer
		c.Add(100)

		// Should be reflected in the reference
		assert.Equal(t, uint64(100), refs[0].Value.Load(), "live reference should reflect updates")

		// Modify via reference
		refs[0].Value.Add(50)

		// Should be reflected in original
		assert.Equal(t, uint64(150), c.Load(), "original should reflect updates via reference")
	})

	t.Run("IDsPreserved", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test", Labels: Labels{"env": "prod"}}
		m.GetOrCreate(id, func() *Counter { return &Counter{} })

		refs := m.Metrics()
		assert.True(t, refs[0].ID.Equals(id), "ID should be preserved")
	})
}

func TestMetricMapConcurrent(t *testing.T) {
	m := NewMetricMap[*Counter]()
	var wg sync.WaitGroup
	n := 100
	ids := make([]MetricID, 10)
	for i := range ids {
		ids[i] = MetricID{Name: fmt.Sprintf("metric%d", i)}
	}

	var createCalls atomic.Int64

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, id := range ids {
				c := m.GetOrCreate(id, func() *Counter {
					createCalls.Add(1)
					return &Counter{}
				})
				c.Inc()
			}
		}()
	}
	wg.Wait()

	// Each metric should be created exactly once
	assert.Equal(t, int64(len(ids)), createCalls.Load(), "each metric should be created exactly once")

	// Each metric should have n increments
	refs := m.Metrics()
	assert.Len(t, refs, len(ids), "should have correct number of metrics")
	for _, ref := range refs {
		assert.Equal(t, uint64(n), ref.Value.Load(), "metric %s should have correct count", ref.ID.Name)
	}
}

func TestMetricMapIntegration(t *testing.T) {
	t.Run("WithGauge", func(t *testing.T) {
		m := NewMetricMap[*Gauge]()
		id := MetricID{Name: "temperature"}
		g := m.GetOrCreate(id, func() *Gauge { return &Gauge{} })
		g.Store(36.6)

		refs := m.Metrics()
		assert.Equal(t, 36.6, refs[0].Value.Load(), "Gauge value should match")
	})

	t.Run("WithHistogram", func(t *testing.T) {
		m := NewMetricMap[*Histogram]()
		id := MetricID{Name: "latency"}
		h := m.GetOrCreate(id, func() *Histogram { return NewHistogram([]float64{10, 50, 100}) })
		h.Observe(25)

		refs := m.Metrics()
		snapshot := refs[0].Value.Snapshot()
		var total uint64
		for _, bucket := range snapshot {
			total += bucket.Count
		}
		assert.Equal(t, uint64(1), total, "Histogram total should match")
	})
}

// Benchmarks

func BenchmarkGetOrCreate(b *testing.B) {
	b.Run("New", func(b *testing.B) {
		for i := range b.N {
			m := NewMetricMap[*Counter]()
			id := MetricID{Name: fmt.Sprintf("metric%d", i)}
			m.GetOrCreate(id, func() *Counter { return &Counter{} })
		}
	})

	b.Run("Existing", func(b *testing.B) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "metric", Labels: Labels{"a": "1"}}
		m.GetOrCreate(id, func() *Counter { return &Counter{} })

		b.ResetTimer()
		for range b.N {
			m.GetOrCreate(id, func() *Counter { return &Counter{} })
		}
	})
}

func BenchmarkGetOrCreateParallel(b *testing.B) {
	m := NewMetricMap[*Counter]()
	ids := make([]MetricID, 100)
	for i := range ids {
		ids[i] = MetricID{Name: fmt.Sprintf("metric%d", i)}
		m.GetOrCreate(ids[i], func() *Counter { return &Counter{} })
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.GetOrCreate(ids[i%len(ids)], func() *Counter { return &Counter{} })
			i++
		}
	})
}

func BenchmarkMetrics(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			m := NewMetricMap[*Counter]()
			for i := range size {
				id := MetricID{Name: fmt.Sprintf("metric%d", i)}
				m.GetOrCreate(id, func() *Counter { return &Counter{} })
			}

			b.ResetTimer()
			for range b.N {
				_ = m.Metrics()
			}
		})
	}
}
