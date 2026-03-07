package metrics

import (
	"hash/fnv"
	"io"
	"sync"
)

// MetricMap is a concurrent-safe hash-addressed collection of metrics.
//
// It maps a composite label set to a single metric instance using FNV-1a
// hashing with collision handling. The hash function and storage layout are
// internal implementation details.
type MetricMap[T any] struct {
	mu sync.RWMutex

	// we need stable pointers here
	entries map[uint64][]metricEntry[T]
}

type metricEntry[T any] struct {
	id     MetricID
	metric T
}

func NewMetricMap[T any]() MetricMap[T] {
	return MetricMap[T]{entries: map[uint64][]metricEntry[T]{}}
}

// GetOrCreate returns the metric for the given label set, creating it via
// create if it does not yet exist. Order of labels is important
func (m *MetricMap[T]) GetOrCreate(id MetricID, create func() T) T {
	h := hashID(id)

	m.mu.RLock()
	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if bucket[idx].id.EqualOrdered(id) {
				m.mu.RUnlock()
				return bucket[idx].metric
			}
		}
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if bucket[idx].id.EqualOrdered(id) {
				return bucket[idx].metric
			}
		}
	}

	m.entries[h] = append(m.entries[h], metricEntry[T]{id: id, metric: create()})

	return m.entries[h][len(m.entries[h])-1].metric
}

// Metrics returns a slice of references of all stored metrics.
func (m *MetricMap[T]) Metrics() []Metric[T] {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Metric[T]
	for _, bucket := range m.entries {
		for i := range bucket {
			out = append(out, Metric[T]{ID: bucket[i].id, Value: bucket[i].metric})
		}
	}
	return out
}

// Hashes metric ID with respect to order of labels
func hashID(id MetricID) uint64 {
	h := fnv.New64a()
	var z [1]byte // zero separator

	_, _ = io.WriteString(h, id.Name)
	_, _ = h.Write(z[:])

	for _, label := range id.Labels {
		_, _ = io.WriteString(h, label.Name)
		_, _ = h.Write(z[:])
		_, _ = io.WriteString(h, label.Value)
		_, _ = h.Write(z[:])
	}

	return h.Sum64()
}
