package balancer

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// Real server description
type Real struct {
	Weight uint16

	DstAddr netip.Addr

	SrcAddr netip.Addr
	SrcMask netip.Addr

	Enabled bool
}

func NewRealFromProto(proto *balancerpb.Real) (*Real, error) {
	dstAddr, err := netip.ParseAddr(string(proto.DstAddr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse destination address: %w", err)
	}

	srcAddr, err := netip.ParseAddr(string(proto.SrcAddr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse source address: %w", err)
	}

	srcMask, err := netip.ParseAddr(string(proto.SrcMask))
	if err != nil {
		return nil, fmt.Errorf("failed to parse source mask: %w", err)
	}

	if proto.Weight > math.MaxUint16 {
		return nil, fmt.Errorf(
			"incorrect weight %d: only values from %d to %d allowed",
			proto.Weight,
			0,
			math.MaxUint16,
		)
	}
	weight := uint16(proto.Weight)
	if weight == 0 {
		return nil, fmt.Errorf("incorrect weight: 0")
	}

	return &Real{
		Weight:  uint16(proto.Weight),
		DstAddr: dstAddr,
		SrcAddr: srcAddr,
		SrcMask: srcMask,
		Enabled: proto.Enabled,
	}, nil
}

func (real *Real) IntoProto() *balancerpb.Real {
	return &balancerpb.Real{
		Weight:  uint32(real.Weight),
		DstAddr: []byte(real.DstAddr.String()),
		SrcAddr: []byte(real.SrcAddr.String()),
		SrcMask: []byte(real.SrcMask.String()),
		Enabled: real.Enabled,
	}
}
