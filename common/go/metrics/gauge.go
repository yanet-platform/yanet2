package metrics

import (
	"math"
	"sync/atomic"

	"github.com/yanet-platform/yanet2/common/commonpb"
)

type Gauge struct {
	// or use go.uber.org/atomic float64?
	bits atomic.Uint64
}

func (g *Gauge) Store(value float64) {
	g.bits.Store(math.Float64bits(value))
}

func (g *Gauge) Load() float64 {
	return math.Float64frombits(g.bits.Load())
}

func (g *Gauge) ToProto() *commonpb.MetricValue {
	return &commonpb.MetricValue{Value: &commonpb.MetricValue_Gauge{Gauge: g.Load()}}
}
