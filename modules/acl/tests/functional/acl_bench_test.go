package acl_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// benchBatchSizes lists the packet-batch sizes swept by each sub-benchmark.
var benchBatchSizes = []int{1, 2, 4, 8, 16, 32}

// BenchmarkACLAllow measures how per-round overhead amortizes with batch size
// through the ACL module for packets that each match a UDP allow rule.
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkACLAllow(b *testing.B) {
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.0"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	eth := layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		DstMAC:       net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	_ = udp.SetNetworkLayerForChecksum(&ip4)

	h, agent, backend := setupACLHarness(b, []string{"port0"})
	applyACLRules(b, backend, "bench", rules)
	wireACLPipeline(b, agent, "port0", "bench")

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				pkts[idx] = pkt
			}
			h.Bench(b, pkts...)
		})
	}
}

// BenchmarkACLDeny measures how per-round overhead amortizes with batch size
// through the ACL module for packets that each match a UDP deny rule,
// exercising the drop-recycling path (result.drop non-empty each round).
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkACLDeny(b *testing.B) {
	rules := []cacl.AclRule{
		deny4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.0"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	eth := layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		DstMAC:       net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	_ = udp.SetNetworkLayerForChecksum(&ip4)

	h, agent, backend := setupACLHarness(b, []string{"port0"})
	applyACLRules(b, backend, "bench", rules)
	wireACLPipeline(b, agent, "port0", "bench")

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				pkts[idx] = pkt
			}
			h.Bench(b, pkts...)
		})
	}
}
