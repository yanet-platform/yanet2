package acl_test

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// TestACL_VacuousPortsFirstMatchWins verifies that rules resolving
// through the without-ports decoding (both port sides vacuous) win or
// lose against port restricted rules exactly by rule order, as a
// single flat decoding would order them, over tcp.
func TestACL_VacuousPortsFirstMatchWins(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}

	base := func(action uint32, ports filter.PortRanges) cacl.ACLRule {
		return cacl.ACLRule{
			Actions:       []cacl.ACLAction{{Kind: action}},
			Devices:       filter.Devices{{Name: "port0"}},
			Src4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Src6s:         []xnetip.BiContiguous{},
			Dst6s:         []xnetip.BiContiguous{},
			SrcPortRanges: allPorts,
			DstPortRanges: ports,
			ProtoRanges:   tcpProto,
		}
	}

	vacuousAllow := base(cacl.ActionAllow, allPorts)
	restrictedDeny := base(cacl.ActionDeny, filter.PortRanges{{From: 200, To: 400}})
	vacuousDeny := base(cacl.ActionDeny, allPorts)
	restrictedAllow := base(cacl.ActionAllow, filter.PortRanges{{From: 200, To: 400}})

	run := func(t *testing.T, rules []cacl.ACLRule, dstPort layers.TCPPort, wantPass bool) {
		t.Helper()
		tcp := layers.TCP{SrcPort: 12345, DstPort: dstPort}
		tcp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		if wantPass {
			require.Len(t, result.Output, 1, "the earlier matching rule must win")
			require.Empty(t, result.Drop)
		} else {
			require.Empty(t, result.Output, "the earlier matching rule must win")
			require.Len(t, result.Drop, 1)
		}
	}

	t.Run("vacuous_allow_before_restricted_deny", func(t *testing.T) {
		// The packet matches both rules; the vacuous allow comes
		// first, so the without-ports decoding must win the merge.
		run(t, []cacl.ACLRule{vacuousAllow, restrictedDeny}, 300, true)
	})

	t.Run("vacuous_deny_before_restricted_allow", func(t *testing.T) {
		run(t, []cacl.ACLRule{vacuousDeny, restrictedAllow}, 300, false)
	})

	t.Run("restricted_wins_when_earlier_and_matching", func(t *testing.T) {
		// Both rules match and the restricted one comes first: the
		// with-ports decoding must win over the later vacuous rule.
		run(t, []cacl.ACLRule{restrictedAllow, vacuousDeny}, 300, true)
		run(t, []cacl.ACLRule{restrictedDeny, vacuousAllow}, 300, false)
	})

	t.Run("vacuous_decides_when_restricted_does_not_match", func(t *testing.T) {
		// The restricted rule does not match the port; only the
		// vacuous rule resolves the packet either way around.
		run(t, []cacl.ACLRule{vacuousAllow, restrictedDeny}, 500, true)
		run(t, []cacl.ACLRule{restrictedDeny, vacuousAllow}, 500, true)
	})
}

// TestACL_VacuousPortsFirstMatchWinsUDP verifies that rules
// resolving through the without-ports decoding win or lose against
// port restricted rules exactly by rule order over udp, whose vacuous
// side resolves on the core classes alone.
func TestACL_VacuousPortsFirstMatchWinsUDP(t *testing.T) {
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

	base := func(action uint32, ports filter.PortRanges) cacl.ACLRule {
		return cacl.ACLRule{
			Actions:       []cacl.ACLAction{{Kind: action}},
			Devices:       filter.Devices{{Name: "port0"}},
			Src4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Src6s:         []xnetip.BiContiguous{},
			Dst6s:         []xnetip.BiContiguous{},
			SrcPortRanges: allPorts,
			DstPortRanges: ports,
			ProtoRanges:   udpProto,
		}
	}

	vacuousAllow := base(cacl.ActionAllow, allPorts)
	restrictedDeny := base(cacl.ActionDeny, filter.PortRanges{{From: 200, To: 400}})
	vacuousDeny := base(cacl.ActionDeny, allPorts)
	restrictedAllow := base(cacl.ActionAllow, filter.PortRanges{{From: 200, To: 400}})

	run := func(t *testing.T, rules []cacl.ACLRule, dstPort layers.UDPPort, wantPass bool) {
		t.Helper()
		udp := layers.UDP{SrcPort: 12345, DstPort: dstPort}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		if wantPass {
			require.Len(t, result.Output, 1, "the earlier matching rule must win")
			require.Empty(t, result.Drop)
		} else {
			require.Empty(t, result.Output, "the earlier matching rule must win")
			require.Len(t, result.Drop, 1)
		}
	}

	t.Run("vacuous_allow_before_restricted_deny", func(t *testing.T) {
		run(t, []cacl.ACLRule{vacuousAllow, restrictedDeny}, 300, true)
	})
	t.Run("vacuous_deny_before_restricted_allow", func(t *testing.T) {
		run(t, []cacl.ACLRule{vacuousDeny, restrictedAllow}, 300, false)
	})
	t.Run("restricted_wins_when_earlier_and_matching", func(t *testing.T) {
		run(t, []cacl.ACLRule{restrictedAllow, vacuousDeny}, 300, true)
		run(t, []cacl.ACLRule{restrictedDeny, vacuousAllow}, 300, false)
	})
	t.Run("vacuous_decides_when_restricted_does_not_match", func(t *testing.T) {
		run(t, []cacl.ACLRule{vacuousAllow, restrictedDeny}, 500, true)
		run(t, []cacl.ACLRule{restrictedDeny, vacuousAllow}, 500, true)
	})
}
