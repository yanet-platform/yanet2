package balancer

import (
	"net/netip"
)

type RealUpdate struct {
	VirtualIp netip.Addr
	Proto     string
	Port      uint16
	RealIp    netip.Addr
	Enable    bool
	Weight    uint32
}

type RealUpdateBuffer struct {
	updates []*RealUpdate
}

func NewRealUpdateBuffer() RealUpdateBuffer {
	return RealUpdateBuffer{
		updates: make([]*RealUpdate, 0),
	}
}

func (buffer *RealUpdateBuffer) Clear() uint32 {
	len := len(buffer.updates)
	buffer.updates = make([]*RealUpdate, 0)
	return uint32(len)
}

func (buffer *RealUpdateBuffer) Append(updates []*RealUpdate) {
	buffer.updates = append(buffer.updates, updates...)
}

func Clone(buffer *RealUpdateBuffer) RealUpdateBuffer {
	updates := make([]*RealUpdate, len(buffer.updates))
	for idx, update := range buffer.updates {
		*updates[idx] = *update
	}
	return RealUpdateBuffer{
		updates: updates,
	}
}
