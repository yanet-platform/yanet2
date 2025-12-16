package decap_test

import (
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// A set of predefined layers for these tests
func testingLayers() (layers.Ethernet, layers.Dot1Q, layers.IPv6, layers.IPv4, layers.ICMPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip6tun := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolIPv4,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      net.ParseIP("1:2:3:4::abcd"),
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.0"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(
			layers.ICMPv4TypeEchoRequest,
			layers.ICMPv4CodeNet,
		),
	}
	return eth, vlan, ip6tun, ip4, icmp

}

func TestDecap_IPIP6(t *testing.T) {
	eth, vlan, ip6, ip4, _ := testingLayers()
	vlan.Type = layers.EthernetTypeIPv4
	ip4.DstIP = net.ParseIP("4.5.6.7")
	ip4.Protocol = layers.IPProtocolIPv6
	ip6.NextHeader = layers.IPProtocolICMPv6
	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &ip6, &icmp)
	t.Log("Origin packet", pkt)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("4.5.6.7/32")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, pkt)
	require.NoError(t, err)
	require.NotEmpty(t, result.Output)
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	vlan.Type = layers.EthernetTypeIPv6
	expectedPkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
		cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
	)
	require.Empty(t, diff)
}

func TestDecap_IPIP(t *testing.T) {
	eth, vlan, _, ip4, icmp := testingLayers()
	vlan.Type = layers.EthernetTypeIPv4
	ip4tun := ip4
	ip4tun.DstIP = net.ParseIP("4.5.6.7")
	ip4tun.Protocol = layers.IPProtocolIPv4

	pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip4tun, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("4.5.6.7/32")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, pkt)
	require.NoError(t, err)
	require.NotEmpty(t, result.Output)
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	vlan.Type = layers.EthernetTypeIPv4
	expectedPkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers())
	require.Empty(t, diff)
}

func TestDecap_IP6IP(t *testing.T) {
	eth, vlan, ip6tun, ip4, icmp := testingLayers()

	pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, pkt)
	require.NoError(t, err)
	require.NotEmpty(t, result.Output)
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	vlan.Type = layers.EthernetTypeIPv4
	expectedPkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers())
	require.Empty(t, diff)
}

func TestDecap_IP6IP6(t *testing.T) {
	eth, vlan, ip6tun, _, _ := testingLayers()

	ip6tun.NextHeader = layers.IPProtocolIPv6

	ip6 := ip6tun
	ip6.NextHeader = layers.IPProtocolICMPv6
	ip6.DstIP = net.ParseIP("::1")

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &ip6, &icmp)
	t.Log("Origin packet", pkt)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, pkt)
	require.NoError(t, err)
	require.NotEmpty(t, result.Output)
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	expectedPkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
		cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
	)
	require.Empty(t, diff)
}

func TestDecap_IP6IP_noVlan(t *testing.T) {
	eth, _, ip6tun, ip4, icmp := testingLayers()

	eth.EthernetType = layers.EthernetTypeIPv6
	pkt := xpacket.LayersToPacket(t, &eth, &ip6tun, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, pkt)
	require.NoError(t, err)
	require.NotEmpty(t, result.Output)
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	eth.EthernetType = layers.EthernetTypeIPv4
	expectedPkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers())
	require.Empty(t, diff)
}

func TestDecap_GRE(t *testing.T) {
	eth, vlan, ip6tun, ip4, icmp := testingLayers()
	ip6noTUN := ip6tun
	ip6noTUN.NextHeader = layers.IPProtocolICMPv4

	ip6tun.NextHeader = layers.IPProtocolGRE
	gre := layers.GRE{
		Protocol: layers.EthernetTypeIPv4,
	}

	input := []gopacket.Packet{
		// 0. The packet to be dropped
		xpacket.LayersToPacket(t, &eth, &vlan, &ip6noTUN, &icmp),
		// 1. Valid ip6-gre-ip tunnel
		xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp),
		// 2. ok checksum
		func() gopacket.Packet {
			gre := gre
			gre.ChecksumPresent = true
			ip4.DstIP = net.ParseIP("1.2.3.2")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 3. ok key
		func() gopacket.Packet {
			gre := gre
			gre.KeyPresent = true
			ip4.DstIP = net.ParseIP("1.2.3.3")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 4. ok SeqPresent
		func() gopacket.Packet {
			gre := gre
			gre.SeqPresent = true
			ip4.DstIP = net.ParseIP("1.2.3.4")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 5. ok all opts present
		func() gopacket.Packet {
			gre := gre
			gre.ChecksumPresent = true
			gre.KeyPresent = true
			gre.SeqPresent = true
			ip4.DstIP = net.ParseIP("1.2.3.5")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 6. Valid ip-gre-ip tunnel
		func() gopacket.Packet {
			vlan := vlan
			vlan.Type = layers.EthernetTypeIPv4
			ip4out := ip4
			ip4out.DstIP = net.ParseIP("1.2.3.4")
			ip4out.Protocol = layers.IPProtocolGRE
			ip4.DstIP = net.ParseIP("1.2.3.6")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4out, &gre, &ip4, &icmp)
		}(),
		// 7. drop version=1
		func() gopacket.Packet {
			gre := gre
			gre.Version = 1
			ip4.DstIP = net.ParseIP("1.2.3.7")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 8. drop version=4
		func() gopacket.Packet {
			gre := gre
			gre.Version = 4
			ip4.DstIP = net.ParseIP("1.2.3.8")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 9. drop - RecursionControl
		func() gopacket.Packet {
			gre := gre
			gre.RecursionControl = 1
			ip4.DstIP = net.ParseIP("1.2.3.9")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 10. drop - RecursionControl
		func() gopacket.Packet {
			gre := gre
			gre.RecursionControl = 4
			ip4.DstIP = net.ParseIP("1.2.3.10")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 11. drop - StrictSourceRoute
		func() gopacket.Packet {
			gre := gre
			gre.StrictSourceRoute = true
			ip4.DstIP = net.ParseIP("1.2.3.11")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
		// 12. drop - RoutingPresent
		func() gopacket.Packet {
			gre := gre
			gre.RoutingPresent = true
			ip4.DstIP = net.ParseIP("1.2.3.12")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &gre, &ip4, &icmp)
		}(),
	}

	vlan.Type = layers.EthernetTypeIPv4
	expected := []gopacket.Packet{
		nil,
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.1.0.0")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.2.3.2")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.2.3.3")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.2.3.4")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.2.3.5")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		func() gopacket.Packet {
			ip4.DstIP = net.ParseIP("1.2.3.6")
			return xpacket.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)
		}(),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	}
	drop := []gopacket.Packet{
		input[0],
		input[7],
		input[8],
		input[9],
		input[10],
		input[11],
		input[12],
	}

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
		xerror.Unwrap(netip.ParsePrefix("1.2.3.4/24")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, input...)
	require.NoError(t, err, "failed to handle packets")
	require.Equal(t, len(expected)-len(drop), len(result.Output), "output")
	require.Equal(t, len(drop), len(result.Drop), "drop")

	outputIdx := 0
	dropIdx := 0
	for idx, p := range expected {
		t.Logf("Origin packet(idx=%d)\n%s", idx, input[idx])
		var resultPkt gopacket.Packet
		if p != nil {
			t.Logf("Expected packet(idx=%d)\n%s", outputIdx, p)
			resultPkt = xpacket.ParseEtherPacket(result.Output[outputIdx])
			outputIdx++
		} else {
			p = drop[dropIdx]
			t.Logf("Expected Dropped packet(idx=%d)\n%s", dropIdx, p)
			resultPkt = xpacket.ParseEtherPacket(result.Drop[dropIdx])
			dropIdx++
		}
		t.Log("Result packet", resultPkt)
		diff := cmp.Diff(
			p.Layers(), resultPkt.Layers(),
			cmpopts.IgnoreUnexported(layers.IPv6{}),
		)
		require.Empty(t, diff, "idx-%d", idx)
	}
}

func TestDecap_Fragment_ipv4(t *testing.T) {
	eth, vlan, ip6tun, ip4, icmp := testingLayers()

	icmpPayload := append(slices.Repeat([]byte("ABCDEFGH123CCCCCCCCC"), 120), []byte("QWERTY123")...)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializeLayers(buf, opts, &icmp, gopacket.Payload(icmpPayload)))
	payload := buf.Bytes()

	fragsize := 1208
	require.Greater(t, len(payload), fragsize)

	input := []gopacket.Packet{}
	expected := []gopacket.Packet{}
	for offset := 0; offset < len(payload); offset += fragsize {
		end := min(offset+fragsize, len(payload))
		payloadFragment := gopacket.Payload(payload[offset:end])

		ip4.Flags = 0
		if end < len(payload) {
			ip4.Flags |= layers.IPv4MoreFragments
		}
		ip4.FragOffset = uint16(offset / 8)

		vlan.Type = layers.EthernetTypeIPv6
		pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &ip4, payloadFragment)
		input = append(input, pkt)

		vlan.Type = layers.EthernetTypeIPv4
		ePkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip4, payloadFragment)
		expected = append(expected, ePkt)

	}

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, input...)
	require.NoError(t, err, "failed to handle packets")
	require.NotEmpty(t, result.Output)
	for idx, expectedPkt := range expected {
		t.Logf("Origin packet idx=%d\n%s", idx, input[idx])
		resultPkt := xpacket.ParseEtherPacket(result.Output[idx])
		t.Logf("Result packet idx=%d\n%s", idx, resultPkt)

		vlan.Type = layers.EthernetTypeIPv4
		t.Logf("Expected packet idx=%d\n%s", idx, expectedPkt)

		diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers())
		require.Empty(t, diff)

	}
}

func TestDecap_Fragment_ipv6(t *testing.T) {
	eth, vlan, ip6tun, ip4, icmp := testingLayers()

	icmpPayload := append(slices.Repeat([]byte("ABCDEFGH123CCCCCCCCC"), 120), []byte("QWERTY123")...)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializeLayers(buf, opts, &ip4, &icmp, gopacket.Payload(icmpPayload)))
	payload := buf.Bytes()

	fragsize := 1208
	require.Greater(t, len(payload), fragsize)

	input := []gopacket.Packet{}

	identification := uint32(0x31337)
	for offset := 0; offset < len(payload); offset += fragsize {
		end := min(offset+fragsize, len(payload))
		payloadFragment := gopacket.Payload(payload[offset:end])

		moreFragments := end < len(payload)

		frag := layers.IPv6Fragment{
			NextHeader:     layers.IPProtocolIPv4,
			Identification: identification,
			FragmentOffset: uint16(offset / 8),
			MoreFragments:  moreFragments,
		}

		vlan.Type = layers.EthernetTypeIPv6
		ip6tun.NextHeader = layers.IPProtocolIPv6Fragment
		pkt := xpacket.LayersToPacket(t, &eth, &vlan, &ip6tun, &frag, &ip4, payloadFragment)
		input = append(input, pkt)
	}
	require.Greater(t, len(input), 1)

	prefixes := []netip.Prefix{
		xerror.Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}

	memCtx := testutils.NewMemoryContext("decap_test", 1<<20)
	defer memCtx.Free()

	m := decapModuleConfig(prefixes, memCtx)

	result, err := decapHandlePackets(m, input...)
	require.NoError(t, err, "failed to handle packets")
	if len(result.Output) > 0 {
		t.Log("Unexpected packet", xpacket.ParseEtherPacket(result.Output[0]))
	}
	require.Equal(t, len(input), len(result.Drop))
	require.Empty(t, result.Output)
}
