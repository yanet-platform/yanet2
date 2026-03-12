package metrics

import (
	"hash/fnv"
	"io"
	"slices"
	"sync"
)

// MetricMap is a concurrent-safe hash-addressed collection of metrics.
//
// It maps a composite label set to a single metric instance using FNV-1a
// hashing with collision handling. The hash function and storage layout are
// internal implementation details.
type MetricMap[T any] struct {
	mu sync.RWMutex

	entries map[uint64][]metricEntry[T]
}

type metricEntry[T any] struct {
	id     MetricID
	metric T
}

func NewMetricMap[T any]() *MetricMap[T] {
	return &MetricMap[T]{entries: map[uint64][]metricEntry[T]{}}
}

func (m *MetricMap[T]) tryGet(id MetricID, h uint64) *T {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if bucket[idx].id.Equals(id) {
				return &bucket[idx].metric
			}
		}
	}

	return nil
}

func (m *MetricMap[T]) create(id MetricID, h uint64, create func() T) T {
	m.mu.Lock()
	defer m.mu.Unlock()

	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if bucket[idx].id.Equals(id) {
				return bucket[idx].metric
			}
		}
	}
	m.entries[h] = append(m.entries[h], metricEntry[T]{id: id.Clone(), metric: create()})
	return m.entries[h][len(m.entries[h])-1].metric
}

// GetOrCreate returns the metric for the given label list, creating it via
// create if it does not yet exist.
func (m *MetricMap[T]) GetOrCreate(id MetricID, create func() T) T {
	h := hashID(id)

	if existent := m.tryGet(id, h); existent != nil {
		return *existent
	}

	return m.create(id, h, create)
}

// Metrics returns a slice of references of all stored metrics.
func (m *MetricMap[T]) Metrics() []Metric[T] {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Metric[T]
	for _, bucket := range m.entries {
		for i := range bucket {
			out = append(out, Metric[T]{ID: bucket[i].id.Clone(), Value: bucket[i].metric})
		}
	}
	return out
}

// Hashes metric ID deterministically.
// Since labels are a map, we sort keys to get stable hashing.
func hashID(id MetricID) uint64 {
	h := fnv.New64a()
	var z [1]byte // zero separator

	_, _ = io.WriteString(h, id.Name)
	_, _ = h.Write(z[:])

	keys := make([]string, 0, len(id.Labels))
	for k := range id.Labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, k := range keys {
		v := id.Labels[k]
		_, _ = io.WriteString(h, k)
		_, _ = h.Write(z[:])
		_, _ = io.WriteString(h, v)
		_, _ = h.Write(z[:])
	}

	return h.Sum64()
}
