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

// metricEntry pairs a stored metric with the ID it was registered under.
type metricEntry[T any] struct {
	id     MetricID
	metric T
}

// MetricFor returns the stored metric when the entry was registered under id.
func (m metricEntry[T]) MetricFor(id MetricID) (T, bool) {
	if m.id.Equals(id) {
		return m.metric, true
	}

	var zero T
	return zero, false
}

// ToMetric converts the entry into a Metric, cloning its ID.
func (m metricEntry[T]) ToMetric() Metric[T] {
	return Metric[T]{ID: m.id.Clone(), Value: m.metric}
}

// Satisfies reports whether pred accepts the entry.
//
// The predicate receives a cloned MetricID so it cannot mutate stored map
// state.
func (m metricEntry[T]) Satisfies(pred func(MetricID, T) bool) bool {
	return pred(m.id.Clone(), m.metric)
}

func NewMetricMap[T any]() *MetricMap[T] {
	return &MetricMap[T]{entries: map[uint64][]metricEntry[T]{}}
}

func (m *MetricMap[T]) tryGet(id MetricID, h uint64) (T, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if metric, ok := bucket[idx].MetricFor(id); ok {
				return metric, true
			}
		}
	}

	var zero T
	return zero, false
}

func (m *MetricMap[T]) create(id MetricID, h uint64, create func() T) T {
	m.mu.Lock()
	defer m.mu.Unlock()

	if bucket, ok := m.entries[h]; ok {
		for idx := range bucket {
			if metric, ok := bucket[idx].MetricFor(id); ok {
				return metric
			}
		}
	}

	metric := create()
	m.entries[h] = append(m.entries[h], metricEntry[T]{id: id.Clone(), metric: metric})
	return metric
}

// GetOrCreate returns the metric for the given label list, creating it via
// create if it does not yet exist.
func (m *MetricMap[T]) GetOrCreate(id MetricID, create func() T) T {
	h := hashID(id)

	if metric, ok := m.tryGet(id, h); ok {
		return metric
	}

	return m.create(id, h, create)
}

// DeleteWhere removes every stored metric whose (ID, metric) satisfies pred.
//
// The predicate receives a cloned MetricID so it cannot mutate stored map
// state. Buckets are compacted in place. Hash buckets that become empty are
// deleted so the map does not accumulate empty slots.
//
// Returns the number of metrics removed.
func (m *MetricMap[T]) DeleteWhere(pred func(MetricID, T) bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for h, bucket := range m.entries {
		kept := slices.DeleteFunc(bucket, func(entry metricEntry[T]) bool {
			return entry.Satisfies(pred)
		})
		removed += len(bucket) - len(kept)

		if len(kept) == 0 {
			delete(m.entries, h)
		} else {
			m.entries[h] = kept
		}
	}

	return removed
}

// Metrics returns a slice of references of all stored metrics.
func (m *MetricMap[T]) Metrics() []Metric[T] {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Metric[T]
	for _, bucket := range m.entries {
		for idx := range bucket {
			out = append(out, bucket[idx].ToMetric())
		}
	}
	return out
}

// IsEmpty reports whether the map holds no metrics.
//
// It is O(1): emptying the map releases its internal storage, so no scan
// is needed.
func (m *MetricMap[T]) IsEmpty() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.entries) == 0
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
