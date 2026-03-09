package metrics

import (
	"math"
	"sync/atomic"
)

type Gauge struct {
	bits atomic.Uint64
}

func (m *Gauge) Store(value float64) {
	m.bits.Store(math.Float64bits(value))
}

func (m *Gauge) Load() float64 {
	return math.Float64frombits(m.bits.Load())
}
