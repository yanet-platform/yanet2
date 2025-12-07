package module

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

////////////////////////////////////////////////////////////////////////////////

// Configuration of the balancer module.
type BalancerAddresses struct {
	SourceIpV4     [4]byte
	SourceIpV6     [16]byte
	DecapAddresses []netip.Addr
}

// NewBalancerAddressesFromProto creates BalancerAddresses from protobuf ModuleConfig.
func NewBalancerAddressesFromProto(
	pb *balancerpb.ModuleConfig,
) (BalancerAddresses, error) {
	if pb == nil {
		return BalancerAddresses{}, fmt.Errorf("module config is nil")
	}

	ba := BalancerAddresses{
		DecapAddresses: make([]netip.Addr, 0, len(pb.DecapAddresses)),
	}

	// Convert source IPv4 (required)
	if len(pb.SourceAddressV4) == 0 {
		return BalancerAddresses{}, fmt.Errorf(
			"source IPv4 address is required",
		)
	}
	if len(pb.SourceAddressV4) != 4 {
		return BalancerAddresses{}, fmt.Errorf(
			"invalid IPv4 source address length: expected 4, got %d",
			len(pb.SourceAddressV4),
		)
	}
	copy(ba.SourceIpV4[:], pb.SourceAddressV4)

	// Convert source IPv6 (required)
	if len(pb.SourceAddressV6) == 0 {
		return BalancerAddresses{}, fmt.Errorf(
			"source IPv6 address is required",
		)
	}
	if len(pb.SourceAddressV6) != 16 {
		return BalancerAddresses{}, fmt.Errorf(
			"invalid IPv6 source address length: expected 16, got %d",
			len(pb.SourceAddressV6),
		)
	}
	copy(ba.SourceIpV6[:], pb.SourceAddressV6)

	// Convert decap addresses
	for i, addrBytes := range pb.DecapAddresses {
		addr, ok := netip.AddrFromSlice(addrBytes)
		if !ok {
			return BalancerAddresses{}, fmt.Errorf(
				"invalid decap address at index %d",
				i,
			)
		}
		ba.DecapAddresses = append(ba.DecapAddresses, addr)
	}

	return ba, nil
}
