package metrics

import (
	"sync/atomic"
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
