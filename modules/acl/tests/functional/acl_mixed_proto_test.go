package acl_test

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// synOrGreProto holds two alternatives of one rule: TCP restricted to
// its SYN flag alone - a transport byte the tcp path discriminates -
// and GRE left whole, a protocol without a transport byte whose
// packets resolve through the plain path.
var synOrGreProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolTCP), filter.ExactSubtype(0x02)),
	filter.NewProtoRange(uint8(layers.IPProtocolGRE), filter.AnySubtype()),
}

// greProto matches GRE alone, whole byte: the plain path owns the
// rule outright.
var greProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolGRE), filter.AnySubtype()),
}

// grePayload carries a minimal GRE header - zero flags and version,
// the IPv4 ethertype - over eight bytes of payload.
var grePayload = append([]byte{0x00, 0x00, 0x08, 0x00}, make([]byte, 8)...)

// TestACL_MixedProtocolAlternatives verifies that the plain path
// keeps the whole byte alternatives of a rule whose other alternative
// restricts a transport subtype: a mixed TCP SYN / GRE deny rule
// denies the TCP SYN packets through the tcp path and the GRE packets
// through the plain path, while the unmatched TCP and UDP traffic
// keeps the fallback allow.
//
// The transport byte restriction must keep the rule out of the plain
// path for its own protocols alone: the packets without the byte -
// GRE here - resolve there, and a rule-wide plain exclusion forwards
// them past the deny. A TCP fragment resolves through the plain path
// too, and the SYN alternative must leave it to the fallback.
func TestACL_MixedProtocolAlternatives(t *testing.T) {
	allProtos := filter.ProtoRanges{{From: 0, To: 65535}}
	unspec4 := []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
	rules := []cacl.ACLRule{
		deny4Rule(unspec4, unspec4, synOrGreProto),
		allow4Rule(unspec4, unspec4, allProtos),
	}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	t.Run("tcp_syn_denied", func(t *testing.T) {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP("192.0.2.1"),
			DstIP:    net.ParseIP("10.0.0.1"),
		}
		tcp := layers.TCP{SrcPort: 12345, DstPort: 80, SYN: true}
		tcp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output, "the SYN alternative must deny the SYN packet")
		require.Len(t, result.Drop, 1)
	})

	t.Run("tcp_other_allowed", func(t *testing.T) {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP("192.0.2.1"),
			DstIP:    net.ParseIP("10.0.0.1"),
		}
		tcp := layers.TCP{SrcPort: 12345, DstPort: 80, ACK: true}
		tcp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "an unsynchronized TCP packet keeps the fallback allow")
		assert.Empty(t, result.Drop)
	})

	t.Run("udp_allowed", func(t *testing.T) {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    net.ParseIP("192.0.2.1"),
			DstIP:    net.ParseIP("10.0.0.1"),
		}
		udp := layers.UDP{SrcPort: 12345, DstPort: 300}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "a UDP packet keeps the fallback allow")
		assert.Empty(t, result.Drop)
	})

	t.Run("gre_denied", func(t *testing.T) {
		gre := rawIPv4Frame(false, false, 0, layers.IPProtocolGRE, grePayload)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandleSegmentedPackets([][]byte{gre})
		require.NoError(t, err)
		assert.Empty(t, result.Output, "the whole byte GRE alternative must deny the GRE packet")
		require.Len(t, result.Drop, 1)
	})

	t.Run("tcp_fragment_allowed", func(t *testing.T) {
		// A non-initial TCP fragment resolves through the plain path
		// without a flags byte: the SYN alternative never matches it,
		// and the plain ranges of the rule leave the protocol out.
		frag := rawIPv4Frame(true, true, 1, layers.IPProtocolTCP, make([]byte, 8))

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandleSegmentedPackets([][]byte{frag})
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "a TCP fragment keeps the fallback allow")
		assert.Empty(t, result.Drop)
	})
}

// TestACL_GREOnlyDenied verifies the control shape of the mixed rule
// above: a GRE-only deny rule keeps denying GRE through the plain
// path, whole byte rule that it is.
func TestACL_GREOnlyDenied(t *testing.T) {
	allProtos := filter.ProtoRanges{{From: 0, To: 65535}}
	unspec4 := []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
	rules := []cacl.ACLRule{
		deny4Rule(unspec4, unspec4, greProto),
		allow4Rule(unspec4, unspec4, allProtos),
	}

	gre := rawIPv4Frame(false, false, 0, layers.IPProtocolGRE, grePayload)

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandleSegmentedPackets([][]byte{gre})
	require.NoError(t, err)
	assert.Empty(t, result.Output, "a whole byte GRE rule must deny the GRE packet")
	require.Len(t, result.Drop, 1)
}

// TestACL_NoProtoRangesDeniesEveryProtocol verifies the wildcard rule
// shape: a rule with no authored protocol ranges constrains no
// protocol - the notion of any the classifiers apply to an empty
// range set - so it denies packets of every protocol through the
// plain path, the fragments and the flagless protocols included.
func TestACL_NoProtoRangesDeniesEveryProtocol(t *testing.T) {
	allProtos := filter.ProtoRanges{{From: 0, To: 65535}}
	unspec4 := []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
	rules := []cacl.ACLRule{
		deny4Rule(unspec4, unspec4, filter.ProtoRanges{}),
		allow4Rule(unspec4, unspec4, allProtos),
	}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	t.Run("gre_denied", func(t *testing.T) {
		gre := rawIPv4Frame(false, false, 0, layers.IPProtocolGRE, grePayload)
		result, err := h.HandleSegmentedPackets([][]byte{gre})
		require.NoError(t, err)
		assert.Empty(t, result.Output)
		require.Len(t, result.Drop, 1)
	})

	t.Run("tcp_denied", func(t *testing.T) {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP("192.0.2.1"),
			DstIP:    net.ParseIP("10.0.0.1"),
		}
		tcp := layers.TCP{SrcPort: 12345, DstPort: 80, SYN: true}
		tcp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output)
		require.Len(t, result.Drop, 1)
	})

	t.Run("udp_denied", func(t *testing.T) {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    net.ParseIP("192.0.2.1"),
			DstIP:    net.ParseIP("10.0.0.1"),
		}
		udp := layers.UDP{SrcPort: 12345, DstPort: 300}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output)
		require.Len(t, result.Drop, 1)
	})
}
