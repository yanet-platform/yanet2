package metrics

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMetricMapGetOrCreate(t *testing.T) {
	t.Run("CreatesNew", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}}}

		var calls int
		c := m.GetOrCreate(id, func() *Counter { calls++; return &Counter{} })

		if calls != 1 {
			t.Errorf("create called %d times, want 1", calls)
		}
		if c == nil {
			t.Fatal("GetOrCreate returned nil")
		}
	})

	t.Run("ReturnsExisting", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test"}

		c1 := m.GetOrCreate(id, func() *Counter { return &Counter{} })
		c1.Inc()

		var calls int
		c2 := m.GetOrCreate(id, func() *Counter { calls++; return &Counter{} })

		if calls != 0 {
			t.Errorf("create called %d times for existing metric", calls)
		}
		if c1 != c2 {
			t.Error("GetOrCreate returned different pointer for same ID")
		}
		if c2.Load() != 1 {
			t.Errorf("existing metric value = %d, want 1", c2.Load())
		}
	})

	t.Run("DifferentIDs", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id1 := MetricID{Name: "metric1"}
		id2 := MetricID{Name: "metric2"}

		c1 := m.GetOrCreate(id1, func() *Counter { return &Counter{} })
		c2 := m.GetOrCreate(id2, func() *Counter { return &Counter{} })

		c1.Add(10)
		c2.Add(20)

		if c1.Load() != 10 || c2.Load() != 20 {
			t.Errorf("metrics not independent: c1=%d, c2=%d", c1.Load(), c2.Load())
		}
	})
}

func TestMetricMapLabelOrder(t *testing.T) {
	m := NewMetricMap[*Counter]()

	id1 := MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}}
	id2 := MetricID{Name: "test", Labels: []Label{{Name: "b", Value: "2"}, {Name: "a", Value: "1"}}}

	c1 := m.GetOrCreate(id1, func() *Counter { return &Counter{} })
	c2 := m.GetOrCreate(id2, func() *Counter { return &Counter{} })

	if c1 == c2 {
		t.Error("different label order should create different metrics")
	}

	c1.Inc()
	if c2.Load() != 0 {
		t.Error("metrics with different label order should be independent")
	}
}

func TestMetricMapMetrics(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		refs := m.Metrics()
		if len(refs) != 0 {
			t.Errorf("Metrics() on empty map = %d items, want 0", len(refs))
		}
	})

	t.Run("ReturnsAll", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		for i := range 5 {
			id := MetricID{Name: fmt.Sprintf("metric%d", i)}
			m.GetOrCreate(id, func() *Counter { return &Counter{} })
		}

		refs := m.Metrics()
		if len(refs) != 5 {
			t.Errorf("Metrics() = %d items, want 5", len(refs))
		}
	})

	t.Run("LiveReferences", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test"}
		c := m.GetOrCreate(id, func() *Counter { return &Counter{} })

		refs := m.Metrics()
		if len(refs) != 1 {
			t.Fatalf("Metrics() = %d items, want 1", len(refs))
		}

		// Modify via original pointer
		c.Add(100)

		// Should be reflected in the reference
		if refs[0].Value.Load() != 100 {
			t.Errorf("live reference not updated: got %d, want 100", refs[0].Value.Load())
		}

		// Modify via reference
		refs[0].Value.Add(50)

		// Should be reflected in original
		if c.Load() != 150 {
			t.Errorf("original not updated via reference: got %d, want 150", c.Load())
		}
	})

	t.Run("IDsPreserved", func(t *testing.T) {
		m := NewMetricMap[*Counter]()
		id := MetricID{Name: "test", Labels: []Label{{Name: "env", Value: "prod"}}}
		m.GetOrCreate(id, func() *Counter { return &Counter{} })

		refs := m.Metrics()
		if !refs[0].ID.EqualOrdered(id) {
			t.Errorf("ID not preserved: got %+v, want %+v", refs[0].ID, id)
		}
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
	if createCalls.Load() != int64(len(ids)) {
		t.Errorf("create called %d times, want %d", createCalls.Load(), len(ids))
	}

	// Each metric should have n increments
	refs := m.Metrics()
	if len(refs) != len(ids) {
		t.Errorf("Metrics() = %d items, want %d", len(refs), len(ids))
	}
	for _, ref := range refs {
		if ref.Value.Load() != uint64(n) {
			t.Errorf("metric %s = %d, want %d", ref.ID.Name, ref.Value.Load(), n)
		}
	}
}

func TestMetricMapIntegration(t *testing.T) {
	t.Run("WithGauge", func(t *testing.T) {
		m := NewMetricMap[*Gauge]()
		id := MetricID{Name: "temperature"}
		g := m.GetOrCreate(id, func() *Gauge { return &Gauge{} })
		g.Store(36.6)

		refs := m.Metrics()
		if refs[0].Value.Load() != 36.6 {
			t.Errorf("Gauge value = %v, want 36.6", refs[0].Value.Load())
		}
	})

	t.Run("WithHistogram", func(t *testing.T) {
		m := NewMetricMap[*Histogram]()
		id := MetricID{Name: "latency"}
		h := m.GetOrCreate(id, func() *Histogram { return NewHistogram([]float64{10, 50, 100}) })
		h.Observe(25)

		refs := m.Metrics()
		total := uint64(0)
		for i := range (*refs[0].Value).Buckets {
			total += (*refs[0].Value).Buckets[i].Load()
		}
		if total != 1 {
			t.Errorf("Histogram total = %d, want 1", total)
		}
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
		id := MetricID{Name: "metric", Labels: []Label{{Name: "a", Value: "1"}}}
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
