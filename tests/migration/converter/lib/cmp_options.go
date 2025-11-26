package lib

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// CmpStdOpts provides standard cmp.Diff options for comparing gopacket layers.
// This ensures consistent packet comparison across all tests (unit tests, PCAP equivalence tests,
// and generated functional tests).
//
// These options:
//   - Ignore unexported fields in gopacket layer structs (internal state)
//   - Ignore BaseLayer fields (gopacket internal bookkeeping)
//   - Ignore DecodeFailure unexported fields (for malformed packet handling)
//
// Usage:
//
//	diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(), lib.CmpStdOpts...)
//	if diff != "" {
//	    t.Errorf("Packet mismatch (-want +got):\n%s", diff)
//	}
var CmpStdOpts = []cmp.Option{
	cmpopts.IgnoreUnexported(
		layers.Ethernet{},
		layers.Dot1Q{},
		layers.IPv4{},
		layers.IPv6{},
		layers.TCP{},
		layers.UDP{},
		layers.ICMPv4{},
		layers.ICMPv6{},
		layers.ICMPv6Echo{},
		layers.ICMPv6RouterSolicitation{},
		layers.ICMPv6RouterAdvertisement{},
		layers.ICMPv6NeighborSolicitation{},
		layers.ICMPv6NeighborAdvertisement{},
		layers.IPv6Destination{},
		layers.IPv6Routing{},
		layers.IPv6HopByHop{},
		layers.IPv6Fragment{},
		layers.GRE{},
		layers.MPLS{},
		layers.ARP{},
		gopacket.DecodeFailure{},
	),
	cmpopts.IgnoreFields(layers.Ethernet{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.Dot1Q{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv4{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv6{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.TCP{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.UDP{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv4{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6Echo{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6RouterSolicitation{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6RouterAdvertisement{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6NeighborSolicitation{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ICMPv6NeighborAdvertisement{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv6Destination{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv6Routing{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv6HopByHop{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.IPv6Fragment{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.GRE{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.MPLS{}, "BaseLayer"),
	cmpopts.IgnoreFields(layers.ARP{}, "BaseLayer"),
}
