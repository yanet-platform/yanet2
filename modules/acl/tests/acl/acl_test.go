package test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

type testEnv struct {
	mock      *mock.YanetMock
	packetGen *PacketGenerator
}

func setupTestEnv(t *testing.T, rules []acl.AclRule) *testEnv {
	setup, err := SetupTest(&TestConfig{
		rules: rules,
	})
	require.NoError(t, err)

	return &testEnv{
		mock:      setup.mock,
		packetGen: NewPacketGenerator(),
	}
}

func (te *testEnv) testPacket(t *testing.T, packetLayers []gopacket.SerializableLayer, expectAllow bool, msg string) {
	packet := xpacket.LayersToPacket(t, packetLayers...)
	result, err := te.mock.HandlePackets(packet)
	require.Nil(t, err, "packet handling failed")

	if expectAllow {
		require.True(t, len(result.Output) == 1, msg)
	} else {
		require.True(t, len(result.Output) == 0, msg)
	}
}

// 1. Basic Allow/Deny Rules
func createBasicRules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:     0, // PASS
			Counter:    "",
			Devices:    []filter.Device{{Name: defaultDeviceName}},
			VlanRanges: []filter.VlanRange{},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.2"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 150, To: 450},
				{From: 600, To: 600},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
		{
			Action:     1, // DENY
			Counter:    "",
			Devices:    []filter.Device{{Name: defaultDeviceName}},
			VlanRanges: []filter.VlanRange{},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.99"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 700, To: 700},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
	}
}

func TestAcl_BasicRules(t *testing.T) {
	te := setupTestEnv(t, createBasicRules())

	t.Run("Allow_UDP_permitted_ports", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 150, nil),
			true, "UDP in allowed range should pass (dst port 150)")
	})

	t.Run("Allow_UDP_permitted_ports_30", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 300, nil),
			true, "UDP in allowed range should pass (dst port 300)")
	})

	t.Run("Allow_UDP_permitted_ports_450", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 450, nil),
			true, "UDP in allowed range should pass (dst port 450)")
	})

	t.Run("Allow_UDP_permitted_ports_600", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 600, nil),
			true, "UDP in allowed range should pass (dst port 600)")
	})

	t.Run("Deny_UDP_blocked_source", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.99", "192.0.3.1", 12345, 700, nil),
			false, "UDP From blocked source should be denied")
	})
}

// 2. Protocol-Specific Rules
func createProtocolRules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.2"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 600, To: 600},
			},
			ProtoRanges: []filter.ProtoRange{
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
		},
		{
			Action:  1, // DENY
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
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
		},
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.2"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 264, To: 264}, // ICMP Echo
			},
		},
	}
}

func TestAcl_ProtocolRules(t *testing.T) {
	te := setupTestEnv(t, createProtocolRules())

	// FAILED
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

	// FAILED
	t.Run("Allow_ICMP_Echo", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeICMPPacket("192.0.2.2", "192.0.3.1",
			layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0), nil),
			true, "ICMP Echo should be allowed")
	})
}

// 3. Port Range Validation
func createPortRangeRules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.2"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 150, To: 450},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
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
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.2", "192.0.3.1", 12345, 600, nil),
			false, "UDP outside port range should be denied")
	})
}

// 4. Subnet Validation Rules
func createSubnetRules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.0"), Mask: netip.MustParseAddr("255.255.255.0")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.3.1"), Mask: netip.MustParseAddr("255.255.255.255")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 600, To: 600},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
		{
			Action:  1, // DENY
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.99.0"), Mask: netip.MustParseAddr("255.255.255.0")},
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
	}
}

func TestAcl_SubnetRules(t *testing.T) {
	te := setupTestEnv(t, createSubnetRules())

	t.Run("Allow_from_subnet", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.100", "192.0.3.1", 12345, 600, nil),
			true, "Packet From allowed subnet should pass")
	})

	// FAILED
	t.Run("Deny_from_blocked_subnet", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.99.1", "192.0.3.1", 12345, 600, nil),
			false, "Packet From blocked subnet should be denied")
	})
}

// 5. IPv6 Rules
func createIPv6Rules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s:   []filter.IPNet4{},
			Dst4s:   []filter.IPNet4{},
			Src6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::1"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			Dst6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::2"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 600, To: 600},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 1536, To: 1791}, // TCP
			},
		},
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s:   []filter.IPNet4{},
			Dst4s:   []filter.IPNet4{},
			Src6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::1"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			Dst6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::2"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 14976, To: 14976}, // ICMPv6 Echo
			},
		},
		{
			Action:  1, // DENY
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s:   []filter.IPNet4{},
			Dst4s:   []filter.IPNet4{},
			Src6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::99"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			Dst6s: []filter.IPNet6{
				{Addr: netip.MustParseAddr("2001:db8::2"), Mask: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
			},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 600, To: 600},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
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

	// FAILED
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
			false, "UDPv6 From blocked source should be denied")
	})
}

// 6. Overlapping Rules Pyramid
func createOverlappingRules() []acl.AclRule {
	return []acl.AclRule{
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.0"), Mask: netip.MustParseAddr("255.255.255.254")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
		{
			Action:  1, // DENY
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.0"), Mask: netip.MustParseAddr("255.255.255.240")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
		{
			Action:  0, // PASS
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.2.0"), Mask: netip.MustParseAddr("255.255.255.0")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
		{
			Action:  1, // DENY
			Devices: []filter.Device{{Name: defaultDeviceName}},
			Src4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("192.0.0.0"), Mask: netip.MustParseAddr("255.255.0.0")},
			},
			Dst4s: []filter.IPNet4{
				{Addr: netip.MustParseAddr("0.0.0.0"), Mask: netip.MustParseAddr("0.0.0.0")},
			},
			Src6s: []filter.IPNet6{},
			Dst6s: []filter.IPNet6{},
			SrcPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			DstPortRanges: []filter.PortRange{
				{From: 0, To: 65535},
			},
			ProtoRanges: []filter.ProtoRange{
				{From: 4352, To: 4607}, // UDP
			},
		},
	}
}

func TestAcl_OverlappingRules(t *testing.T) {
	te := setupTestEnv(t, createOverlappingRules())

	t.Run("Allow_/31", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.1", "192.0.3.1", 12345, 150, nil),
			true, "Packet From /31 should be allowed")
	})

	// FAILED
	t.Run("Deny_/28", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.5", "192.0.3.1", 12345, 150, nil),
			false, "Packet From /28 should be denied")
	})

	t.Run("Allow_/24", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.2.100", "192.0.3.1", 12345, 150, nil),
			true, "Packet From /24 should be allowed")
	})

	// FAILED
	t.Run("Deny_/16", func(t *testing.T) {
		te.testPacket(t, te.packetGen.MakeUDPPacket("192.0.10.1", "192.0.3.1", 12345, 150, nil),
			false, "Packet From /16 should be denied")
	})
}
