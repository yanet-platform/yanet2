package mbalancer

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type RealUpdate struct {
	VirtualIp netip.Addr
	Proto     TransportProto
	Port      uint16
	RealIp    netip.Addr
	Enable    bool
	Weight    uint32
}

func NewRealUpdateFromProto(update *balancerpb.RealUpdate) (*RealUpdate, error) {
	if update.Weight > math.MaxUint16 {
		return nil, fmt.Errorf("real weight can not exceed %d", math.MaxUint16)
	}
	vip, err := netip.ParseAddr(string(update.VirtualIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse virtual ip: %w", err)
	}
	realIp, err := netip.ParseAddr(string(update.RealIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse real ip: %w", err)
	}
	return &RealUpdate{
		VirtualIp: vip,
		Proto:     TransportProtoFromProto(update.Proto),
		Port:      uint16(update.Port),
		RealIp:    realIp,
		Enable:    update.Enable,
		Weight:    update.Weight,
	}, nil
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
