package lib

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

////////////////////////////////////////////////////////////////////////////////

type RealUpdate struct {
	Real   RealIdentifier
	Weight uint16
	Enable bool
}

func NewRealUpdateFromProto(
	update *balancerpb.RealUpdate,
) (*RealUpdate, error) {
	if update.Weight > math.MaxUint16 {
		return nil, fmt.Errorf(
			"incorrect real weight: real weight can not exceed %d",
			math.MaxUint16,
		)
	}
	vip, ok := netip.AddrFromSlice(update.VirtualIp)
	if !ok {
		return nil, fmt.Errorf("incorrect virtual service IP")
	}
	realIp, ok := netip.AddrFromSlice(update.RealIp)
	if !ok {
		return nil, fmt.Errorf("incorrect real ip")
	}

	return &RealUpdate{
		Real: RealIdentifier{
			Vs: VsIdentifier{
				Ip:    vip,
				Port:  uint16(update.Port),
				Proto: NewProtoFromProto(update.Proto),
			},
			Ip: realIp,
		},
		Weight: uint16(update.Weight),
		Enable: update.Enable,
	}, nil
}

type RealUpdateBuffer struct {
	updates []RealUpdate
}

func NewRealUpdateBuffer() RealUpdateBuffer {
	return RealUpdateBuffer{
		updates: []RealUpdate{},
	}
}

func (buffer *RealUpdateBuffer) Clear() []RealUpdate {
	updates := buffer.updates
	buffer.updates = []RealUpdate{}
	return updates
}

func (buffer *RealUpdateBuffer) Append(updates []RealUpdate) {
	buffer.updates = append(buffer.updates, updates...)
}
