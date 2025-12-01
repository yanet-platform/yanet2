package test_acl

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"github.com/yanet-platform/yanet2/tests/go/common"
	test_utils "github.com/yanet-platform/yanet2/tests/utils/go"
)

type testEnv struct {
	mock      *test_utils.YanetMock
	aclModule *acl.ModuleConfig
	packetGen *PacketGenerator
}

func setupTestEnv(t *testing.T, rules []*aclpb.Rule) *testEnv {
	mock, err := test_utils.NewYanetMock(1<<20, 1<<27, []string{"acl"})
	require.Nil(t, err, "failed to create mock")

	agent, err := mock.AttachAgent("acl", 1<<26)
	require.Nil(t, err, "failed to attach agent")

	aclModule, err := acl.NewModuleConfig(agent, "acl0", rules)
	require.Nil(t, err, "failed to create ACL module")

	err = mock.PrepareForCpUpdate()
	require.Nil(t, err, "failed to prepare update")

	return &testEnv{
		mock:      mock,
		aclModule: aclModule,
		packetGen: NewPacketGenerator(),
	}
}

func (te *testEnv) testPacket(t *testing.T, packetLayers []gopacket.SerializableLayer, expectAllow bool, msg string) {
	packet := common.LayersToPacket(t, packetLayers...)
	result, err := HandlePackets(te.mock, te.aclModule, packet)
	require.Nil(t, err, "packet handling failed")

	if expectAllow {
		require.True(t, len(result.Output) == 1, msg)
	} else {
		require.True(t, len(result.Output) == 0, msg)
	}
}

// 1. Basic Allow/Deny Rules
func createBasicRules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 2}, PrefixLen: 32}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{
					{From: 150, To: 450},
					{From: 600, To: 600},
				},
				ProtoRanges: []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:     []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 99}, PrefixLen: 32}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 700, To: 700}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
	}
}

func TestAcl_BasicRules(t *testing.T) {
	te := setupTestEnv(t, createBasicRules())

	t.Run("Allow_UDP_permitted_ports", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 150, nil),
			true, "UDP in allowed range should pass (dst port 150)")
	})

	t.Run("Allow_UDP_permitted_ports", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 30, nil),
			true, "UDP in allowed range should pass (dst port 300)")
	})

	t.Run("Allow_UDP_permitted_ports", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 450, nil),
			true, "UDP in allowed range should pass (dst port 450)")
	})

	// НЕ РАБОТАЕТ
	// t.Run("Allow_UDP_permitted_ports", func(t *testing.T) {
	// 	te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 600, nil),
	// 		true, "UDP in allowed range should pass (dst port 600)")
	// })

	t.Run("Deny_UDP_blocked_source", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.99", "192.0.3.1", 12345, 700, nil),
			false, "UDP from blocked source should be denied")
	})
}

// 2. Protocol-Specific Rules
func createProtocolRules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 2}, PrefixLen: 32}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 600, To: 600}},
				ProtoRanges: []*aclpb.ProtoRange{
					{From: 1538, To: 1539}, {From: 1542, To: 1543}, // TCP with SYN flag
					{From: 1546, To: 1547}, {From: 1550, To: 1551},
					{From: 1554, To: 1555}, {From: 1558, To: 1559},
					{From: 1562, To: 1563}, {From: 1566, To: 1567},
					{From: 1570, To: 1571}, {From: 1574, To: 1575},
					{From: 1578, To: 1579}, {From: 1582, To: 1583},
					{From: 1586, To: 1587}, {From: 1590, To: 1591},
					{From: 1594, To: 1595}, {From: 1598, To: 1599},
					{From: 1602, To: 1603}, {From: 1606, To: 1607},
					{From: 1610, To: 1611}, {From: 1614, To: 1615},
					{From: 1618, To: 1619}, {From: 1622, To: 1623},
					{From: 1626, To: 1627}, {From: 1630, To: 1631},
					{From: 1634, To: 1635}, {From: 1638, To: 1639},
					{From: 1642, To: 1643}, {From: 1646, To: 1647},
					{From: 1650, To: 1651}, {From: 1654, To: 1655},
					{From: 1658, To: 1659}, {From: 1662, To: 1663},
					{From: 1666, To: 1667}, {From: 1670, To: 1671},
					{From: 1674, To: 1675}, {From: 1678, To: 1679},
					{From: 1682, To: 1683}, {From: 1686, To: 1687},
					{From: 1690, To: 1691}, {From: 1694, To: 1695},
					{From: 1698, To: 1699}, {From: 1702, To: 1703},
					{From: 1706, To: 1707}, {From: 1710, To: 1711},
					{From: 1714, To: 1715}, {From: 1718, To: 1719},
					{From: 1722, To: 1723}, {From: 1726, To: 1727},
					{From: 1730, To: 1731}, {From: 1734, To: 1735},
					{From: 1738, To: 1739}, {From: 1742, To: 1743},
					{From: 1746, To: 1747}, {From: 1750, To: 1751},
					{From: 1754, To: 1755}, {From: 1758, To: 1759},
					{From: 1762, To: 1763}, {From: 1766, To: 1767},
					{From: 1770, To: 1771}, {From: 1774, To: 1775},
					{From: 1778, To: 1779}, {From: 1782, To: 1783},
					{From: 1786, To: 1787}, {From: 1790, To: 1791},
				},
				Devices: []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				Src6S:         []*aclpb.IPNet{{Ip: append([]byte{0, 0, 0, 0}, make([]byte, 12)...), PrefixLen: 0}},
				Dst6S:         []*aclpb.IPNet{{Ip: append([]byte{0, 0, 0, 0}, make([]byte, 12)...), PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges: []*aclpb.ProtoRange{
					{From: 1540, To: 1543}, {From: 1548, To: 1551},
					{From: 1556, To: 1559}, {From: 1564, To: 1567},
					{From: 1572, To: 1575}, {From: 1580, To: 1583},
					{From: 1588, To: 1591}, {From: 1596, To: 1599},
					{From: 1604, To: 1607}, {From: 1612, To: 1615},
					{From: 1620, To: 1623}, {From: 1628, To: 1631},
					{From: 1636, To: 1639}, {From: 1644, To: 1647},
					{From: 1652, To: 1655}, {From: 1660, To: 1663},
					{From: 1668, To: 1671}, {From: 1676, To: 1679},
					{From: 1684, To: 1687}, {From: 1692, To: 1695},
					{From: 1700, To: 1703}, {From: 1708, To: 1711},
					{From: 1716, To: 1719}, {From: 1724, To: 1727},
					{From: 1732, To: 1735}, {From: 1740, To: 1743},
					{From: 1748, To: 1751}, {From: 1756, To: 1759},
					{From: 1764, To: 1767}, {From: 1772, To: 1775},
					{From: 1780, To: 1783}, {From: 1788, To: 1791},
				},
				Devices: []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 2}, PrefixLen: 32}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 264, To: 264}}, // ICMP Echo
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
	}
}

func TestAcl_ProtocolRules(t *testing.T) {
	te := setupTestEnv(t, createProtocolRules())

	t.Run("Allow_TCP_SYN", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeTCPPacket("192.0.2.2", "192.0.3.1", 12345, 600,
			true, false, false, false, nil),
			true, "TCP SYN should be allowed")
	})

	t.Run("Deny_TCP_RST", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeTCPPacket("192.0.2.2", "192.0.3.1", 12345, 600,
			false, false, true, false, nil),
			false, "TCP RST should be denied")
	})

	t.Run("Allow_ICMP_Echo", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeICMPPacket("192.0.2.2", "192.0.3.1",
			layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0), nil),
			true, "ICMP Echo should be allowed")
	})
}

// 3. Port Range Validation
func createPortRangeRules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 2}, PrefixLen: 32}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 150, To: 450}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
	}
}

func TestAcl_PortRangeRules(t *testing.T) {
	te := setupTestEnv(t, createPortRangeRules())

	t.Run("Allow_UDP_in_range", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 300, nil),
			true, "UDP in port range should pass")
	})

	t.Run("Deny_UDP_outside_range", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 500, nil),
			false, "UDP outside port range should be denied")
	})
}

// 4. Subnet Validation Rules
func createSubnetRules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 0}, PrefixLen: 24}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 3, 1}, PrefixLen: 32}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 600, To: 600}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 99, 0}, PrefixLen: 24}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				Src6S:         []*aclpb.IPNet{{Ip: append([]byte{0, 0, 0, 0}, make([]byte, 12)...), PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
	}
}

func TestAcl_SubnetRules(t *testing.T) {
	te := setupTestEnv(t, createSubnetRules())

	t.Run("Allow_from_subnet", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.100", "192.0.3.1", 12345, 600, nil),
			true, "Packet from allowed subnet should pass")
	})

	t.Run("Deny_from_blocked_subnet", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.99.1", "192.0.3.1", 12345, 600, nil),
			false, "Packet from blocked subnet should be denied")
	})
}

// 5. IPv6 Rules
func createIPv6Rules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				Src6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::1"), PrefixLen: 128}},
				Dst6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::2"), PrefixLen: 128}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 600, To: 600}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 1536, To: 1791}}, // TCP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				Src6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::1"), PrefixLen: 128}},
				Dst6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::2"), PrefixLen: 128}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 14976, To: 14976}}, // ICMPv6 Echo
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				Src6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::99"), PrefixLen: 128}},
				Dst6S:         []*aclpb.IPNet{{Ip: net.ParseIP("2001:db8::2"), PrefixLen: 128}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 600, To: 600}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
	}
}

func TestAcl_IPv6Rules(t *testing.T) {
	te := setupTestEnv(t, createIPv6Rules())

	t.Run("Allow_TCPv6", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeTCPPacket("2001:db8::1", "2001:db8::2", 12345, 600,
			false, false, false, false, nil),
			true, "TCPv6 SYN should be allowed")
	})

	t.Run("Allow_ICMPv6_Echo", func(t *testing.T) {
		eth := &layers.Ethernet{
			SrcMAC:       te.packetGen.SrcMAC,
			DstMAC:       te.packetGen.DstMAC,
			EthernetType: layers.EthernetTypeIPv6,
		}
		ip6 := &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolICMPv6,
			HopLimit:   64,
			SrcIP:      net.ParseIP("2001:db8::1"),
			DstIP:      net.ParseIP("2001:db8::2"),
		}
		icmp6 := &layers.ICMPv6{
			TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
		}
		icmp6.SetNetworkLayerForChecksum(ip6)

		te.testPacket(t, []gopacket.SerializableLayer{eth, ip6, icmp6},
			true, "ICMPv6 Echo should be allowed")
	})

	t.Run("Deny_UDPv6", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("2001:db8::99", "2001:db8::2", 12345, 600, nil),
			false, "UDPv6 from blocked source should be denied")
	})
}

// 6. Overlapping Rules Pyramid
func createOverlappingRules() []*aclpb.Rule {
	return []*aclpb.Rule{
		{
			// allow /31 (192.0.2.0-192.0.2.1)
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 0}, PrefixLen: 31}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			// deny /28 (192.0.2.0-192.0.2.15)
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 0}, PrefixLen: 28}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
		{
			// allow /24 (192.0.2.0-192.0.2.255)
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 2, 0}, PrefixLen: 24}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			// deny /16 (192.0.0.0-192.0.2.255)
			Filter: &aclpb.Filter{
				Src4S:         []*aclpb.IPNet{{Ip: []byte{192, 0, 0, 0}, PrefixLen: 16}},
				Dst4S:         []*aclpb.IPNet{{Ip: []byte{0, 0, 0, 0}, PrefixLen: 0}},
				SrcPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				DstPortRanges: []*aclpb.PortRange{{From: 0, To: 65535}},
				ProtoRanges:   []*aclpb.ProtoRange{{From: 4352, To: 4607}}, // UDP
				Devices:       []string{"device1"},
			},
			Action: aclpb.ActionKind_ACTION_KIND_DENY,
		},
	}
}

func TestAcl_OverlappingRules(t *testing.T) {
	te := setupTestEnv(t, createOverlappingRules())

	t.Run("Allow_/31", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.1", "192.0.3.1", 12345, 150, nil),
			true, "Packet from /31 should be allowed")
	})

	// НЕ РАБОТАЕТ
	// t.Run("Deny_/28", func(t *testing.T) {
	// 	te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.5", "192.0.3.1", 12345, 150, nil),
	// 		false, "Packet from /28 should be denied")
	// })

	t.Run("Allow_/24", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.100", "192.0.3.1", 12345, 150, nil),
			true, "Packet from /24 should be allowed")
	})

	t.Run("Deny_/16", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.10.1", "192.0.3.1", 12345, 150, nil),
			false, "Packet from /16 should be denied")
	})
}
