package metrics

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/common/commonpb"
)

type Counter struct {
	value atomic.Uint64
}

func (c *Counter) Add(value uint64) uint64 {
	return c.value.Add(value)
}

func (c *Counter) Inc() uint64 {
	return c.Add(1)
}

func (c *Counter) Load() uint64 {
	return c.value.Load()
}

func (c *Counter) ToProto() *commonpb.MetricValue {
	return &commonpb.MetricValue{Value: &commonpb.MetricValue_Counter{Counter: c.Load()}}
}
