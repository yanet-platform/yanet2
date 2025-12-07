package module

import (
	"net/netip"
)

////////////////////////////////////////////////////////////////////////////////

// Identifier of the real.
type RealIdentifier struct {
	// Virtual service which is related to real.
	Vs VsIdentifier

	// Ip address of the real.
	Ip netip.Addr
}

// Real of the virtual service.
type Real struct {
	// Index of the real in balancer module
	// config state registry.
	RegistryIdx uint64

	// Identifier of the real.
	Identifier RealIdentifier

	// Weight of the real, corresponds
	// to the logical instances count of the real
	// in the virtual service ring.
	Weight uint16

	// Effective weight of the real,
	// calculated with WLC if it is enabled
	// for the virtual service. If not, it equals zero.
	EffectiveWeight uint16

	// Source address of real,
	// use it forwarding packet to real
	// together with `SrcMask`.
	SrcAddr netip.Addr

	// Source mask of real.
	// When we forward packet to real,
	// source address of packet is calculated
	// as (UserSrc & ~SrcMask) | (SrcAddr & SrcMask)
	SrcMask netip.Addr

	// We send traffic to real only if
	// it is enabled.
	Enabled bool
}
