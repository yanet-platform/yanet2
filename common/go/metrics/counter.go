package metrics

import (
	"sync/atomic"
)

type Counter struct {
	value atomic.Uint64
}

func (m *Counter) Add(value uint64) uint64 {
	return m.value.Add(value)
}

func (m *Counter) Inc() uint64 {
	return m.Add(1)
}

func (m *Counter) Load() uint64 {
	return m.value.Load()
}
