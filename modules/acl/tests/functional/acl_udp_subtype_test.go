package acl_test

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xnetip "github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	xpacket "github.com/yanet-platform/yanet2/common/go/xpacket"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// udpSubtypeZeroProto matches the zero subtype of UDP only: a UDP
// packet resolves at the zero subtype, so the range matches every
// real UDP packet and nothing else - no other protocol and no
// fragment, which resolve outside the covered span.
var udpSubtypeZeroProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.ExactSubtype(0)),
}

// udpTcpMixedProto restricts UDP to its zero subtype and leaves TCP
// whole: the rule matches every real UDP and TCP packet through two
// different paths of the same rule.
var udpTcpMixedProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.ExactSubtype(0)),
	filter.NewProtoRange(uint8(layers.IPProtocolTCP), filter.AnySubtype()),
}

// TestACL_UDPSubtypeZero verifies that a rule restricting UDP to a
// partial subtype span containing the zero subtype still matches real
// UDP packets, which carry no subtype byte and resolve at zero: the
// rule must deny UDP traffic while the same rule never matches a UDP
// fragment or, in the mixed shape, keeps matching the whole TCP span.
func TestACL_UDPSubtypeZero(t *testing.T) {
	allProtos := filter.ProtoRanges{{From: 0, To: 65535}}
	deny := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		udpSubtypeZeroProto,
	)
	deny.Actions = []cacl.ACLAction{{Kind: cacl.ActionDeny}}
	rules := []cacl.ACLRule{
		deny,
		allow4Rule(
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			allProtos,
		),
	}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}

	t.Run("udp_denied", func(t *testing.T) {
		udp := layers.UDP{SrcPort: 12345, DstPort: 300}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output, "a real UDP packet resolves at the zero subtype and must be denied")
		require.Len(t, result.Drop, 1)
	})

	t.Run("udp_fragment_allowed", func(t *testing.T) {
		// A non-initial UDP fragment resolves through the plain path,
		// where the partial subtype span never matches.
		frag := rawIPv4Frame(false, true, 1, layers.IPProtocolUDP, make([]byte, 8))

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandleSegmentedPackets([][]byte{frag})
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "a UDP fragment never matches a partial subtype span")
		assert.Empty(t, result.Drop)
	})
}

// TestACL_UDPSubtypeZeroMixedTCP verifies the mixed range shape: a
// rule restricting UDP to its zero subtype while leaving TCP whole
// must deny real UDP packets through the udp path and TCP packets
// through the tcp path of the same rule.
func TestACL_UDPSubtypeZeroMixedTCP(t *testing.T) {
	allProtos := filter.ProtoRanges{{From: 0, To: 65535}}
	deny := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		udpTcpMixedProto,
	)
	deny.Actions = []cacl.ACLAction{{Kind: cacl.ActionDeny}}
	rules := []cacl.ACLRule{
		deny,
		allow4Rule(
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			allProtos,
		),
	}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version: 4,
		TTL:     64,
		SrcIP:   net.ParseIP("192.0.2.1"),
		DstIP:   net.ParseIP("10.0.0.1"),
	}

	for _, tc := range []struct {
		name     string
		protocol layers.IPProtocol
	}{
		{"udp_denied", layers.IPProtocolUDP},
		{"tcp_denied", layers.IPProtocolTCP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip4.Protocol = tc.protocol
			var transport gopacket.SerializableLayer
			switch tc.protocol {
			case layers.IPProtocolUDP:
				udp := layers.UDP{SrcPort: 12345, DstPort: 300}
				udp.SetNetworkLayerForChecksum(&ip4)
				transport = &udp
			case layers.IPProtocolTCP:
				tcp := layers.TCP{SrcPort: 12345, DstPort: 300}
				tcp.SetNetworkLayerForChecksum(&ip4)
				transport = &tcp
			}
			pkt := xpacket.LayersToPacket(t, &eth, &ip4, transport)

			h, agent, backend := setupACLHarness(t, []string{"port0"})
			applyACLRules(t, backend, "test", rules)
			wireACLPipeline(t, agent, "port0", "test")

			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)
			assert.Empty(t, result.Output, "the rule must deny the packet")
			require.Len(t, result.Drop, 1)
		})
	}
}
