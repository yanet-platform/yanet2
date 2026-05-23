package route_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// encodedDeviceID is the encoded output device value placed in mc_index. Any
// non-0xFFFF value signals a valid output device to the route handler.
const encodedDeviceID = uint64(7)

// invalidDeviceID is the sentinel that causes the route handler to drop the
// packet (mirrors (uint16_t)-1 cast to uint64_t).
const invalidDeviceID = uint64(0xFFFF)

// testingEtherLayers returns a reusable set of Ethernet and IP layers for
// building route test packets.
func testingEtherLayers() (layers.Ethernet, layers.IPv4, layers.IPv6, layers.ICMPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("::1"),
		DstIP:      net.ParseIP("2001:db8::1"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	return eth, ip4, ip6, icmp
}

// routeNextHop is the nexthop definition used across single-hop tests.
// Packets forwarded via this hop will have their Ethernet header rewritten
// with these MACs.
var routeNextHop = FIBNexthop{
	DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:01")),
	SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:01")),
	Device: "veth1",
}

func TestRoute_IPv4_Forward(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()

	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})

	result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected one forwarded packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	// The Ethernet header must be rewritten with the next-hop MACs.
	ethOut := layers.Ethernet{
		SrcMAC:       routeNextHop.SrcMAC,
		DstMAC:       routeNextHop.DstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	// TTL must be decremented by one.
	ip4Out := ip4
	ip4Out.TTL = 63
	expectedPkt := xpacket.LayersToPacket(t, &ethOut, &ip4Out, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(), cmpopts.IgnoreUnexported(layers.ICMPv4{}))
	require.Empty(t, diff)

	// Verify that the IPv4 layer parsed cleanly (gopacket validates checksum on decode).
	ip4Layer := resultPkt.Layer(layers.LayerTypeIPv4)
	require.NotNil(t, ip4Layer, "IPv4 layer must be present in result")
}

func TestRoute_IPv6_Forward(t *testing.T) {
	_, _, ip6, _ := testingEtherLayers()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
	t.Log("Origin packet", pkt)

	prefix := xerror.Unwrap(netip.ParsePrefix("2001:db8::/32"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})

	result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected one forwarded packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	ethOut := layers.Ethernet{
		SrcMAC:       routeNextHop.SrcMAC,
		DstMAC:       routeNextHop.DstMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}
	// HopLimit must be decremented by one.
	ip6Out := ip6
	ip6Out.HopLimit = 63
	expectedPkt := xpacket.LayersToPacket(t, &ethOut, &ip6Out, &icmp6)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
		cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
	)
	require.Empty(t, diff)
}

func TestRoute_TTL_Drop(t *testing.T) {
	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	cases := []struct {
		name            string
		ttl             uint8
		expectForwarded bool
	}{
		{name: "ttl_zero_drop", ttl: 0, expectForwarded: false},
		{name: "ttl_one_drop", ttl: 1, expectForwarded: false},
		{name: "ttl_two_forward", ttl: 2, expectForwarded: true},
		{name: "ttl_64_forward", ttl: 64, expectForwarded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eth, ip4, _, icmp := testingEtherLayers()
			ip4.TTL = tc.ttl

			pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
			t.Logf("Origin packet idx=0\n%s", pkt)

			backend := setupRouteBackend(t)
			mc := applyFIB(t, backend, "test", []FIBEntry{
				{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
			})

			result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
			require.NoError(t, err)

			if tc.expectForwarded {
				require.Len(t, result.Output, 1, "expected forwarded packet")
				require.Empty(t, result.Drop, "expected no dropped packets")
				resultPkt := xpacket.ParseEtherPacket(result.Output[0])
				ip4Layer := resultPkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
				require.Equal(t, uint8(tc.ttl-1), ip4Layer.TTL)
			} else {
				require.Empty(t, result.Output, "expected no forwarded packets")
				require.Len(t, result.Drop, 1, "expected dropped packet")
			}
		})
	}
}

func TestRoute_HopLimit_Drop(t *testing.T) {
	prefix := xerror.Unwrap(netip.ParsePrefix("2001:db8::/32"))

	cases := []struct {
		name            string
		hopLimit        uint8
		expectForwarded bool
	}{
		{name: "hop_limit_zero_drop", hopLimit: 0, expectForwarded: false},
		{name: "hop_limit_one_drop", hopLimit: 1, expectForwarded: false},
		{name: "hop_limit_two_forward", hopLimit: 2, expectForwarded: true},
		{name: "hop_limit_64_forward", hopLimit: 64, expectForwarded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eth := layers.Ethernet{
				SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
				DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
				EthernetType: layers.EthernetTypeIPv6,
			}
			_, _, ip6, _ := testingEtherLayers()
			ip6.HopLimit = tc.hopLimit
			icmp6 := layers.ICMPv6{
				TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
			}
			icmp6.SetNetworkLayerForChecksum(&ip6)

			pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
			t.Logf("Origin packet idx=0\n%s", pkt)

			backend := setupRouteBackend(t)
			mc := applyFIB(t, backend, "test", []FIBEntry{
				{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
			})

			result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
			require.NoError(t, err)

			if tc.expectForwarded {
				require.Len(t, result.Output, 1, "expected forwarded packet")
				require.Empty(t, result.Drop, "expected no dropped packets")
				resultPkt := xpacket.ParseEtherPacket(result.Output[0])
				ip6Layer := resultPkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
				require.Equal(t, uint8(tc.hopLimit-1), ip6Layer.HopLimit)
			} else {
				require.Empty(t, result.Output, "expected no forwarded packets")
				require.Len(t, result.Drop, 1, "expected dropped packet")
			}
		})
	}
}

func TestRoute_NoMatch_Drop(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()
	// Destination is not covered by any prefix in the LPM.
	ip4.DstIP = net.ParseIP("10.99.99.99")

	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})

	result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "unrouted packet must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}

func TestRoute_NonIP_Drop(t *testing.T) {
	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   eth.SrcMAC,
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("10.0.0.2").To4(),
	}

	pkt := xpacket.LayersToPacket(t, &eth, &arp)
	t.Log("Origin packet", pkt)

	result, err := routeHandlePackets(mc, mcIndexFor(encodedDeviceID), pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "non-IP packet must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}

func TestRoute_ECMP_HashSelection(t *testing.T) {
	// Two nexthops share one prefix via a two-entry route_list.
	hop0 := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:01")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:01")),
		Device: "veth1",
	}
	hop1 := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:02")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:02")),
		Device: "veth2",
	}

	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{hop0, hop1}},
	})

	// Slots correspond to hop0 ("veth1") and hop1 ("veth2") in nexthop
	// slice order — that order determines device registration order.
	mcIndex := mcIndexFor(encodedDeviceID, encodedDeviceID+1)

	// Build 4 identical packets. Inject explicit hash values: two with hash=0
	// (route_list[0 % 2] → hop0) and two with hash=1 (route_list[1 % 2] → hop1),
	// confirming both arms are reachable.
	eth, ip4, _, icmp := testingEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pkts := []gopacket.Packet{pkt, pkt, pkt, pkt}
	hashes := []uint32{0, 1, 0, 1}

	result, err := routeHandlePacketsWithHashes(mc, mcIndex, hashes, pkts...)
	require.NoError(t, err)
	require.Len(t, result.Output, 4, "all packets must be forwarded")
	require.Empty(t, result.Drop)

	// Verify both nexthops were selected exactly twice each.
	hop0Count, hop1Count := 0, 0
	for _, raw := range result.Output {
		resultPkt := xpacket.ParseEtherPacket(raw)
		ethLayer := resultPkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
		switch ethLayer.DstMAC.String() {
		case hop0.DstMAC.String():
			hop0Count++
		case hop1.DstMAC.String():
			hop1Count++
		}
	}
	t.Logf("ECMP distribution: hop0=%d hop1=%d", hop0Count, hop1Count)
	require.Equal(t, 2, hop0Count, "hop0 must be selected for hash=0 packets")
	require.Equal(t, 2, hop1Count, "hop1 must be selected for hash=1 packets")
}

func TestRoute_DeviceTranslation_Drop(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()

	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := xerror.Unwrap(netip.ParsePrefix("192.168.1.0/24"))

	backend := setupRouteBackend(t)
	mc := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})

	result, err := routeHandlePackets(mc, mcIndexFor(invalidDeviceID), pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "packet with invalid device translation must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}
