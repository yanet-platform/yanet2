package nat64_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

// testingLayers returns predefined layers for tests
func testingLayers() (layers.Ethernet, layers.IPv6, layers.IPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::192.0.2.2"),
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("192.0.2.2"),
	}

	return eth, ip6, ip4
}

// TestNat64_ICMP_v6_to_v4_Echo tests translation of ICMPv6 Echo Request to ICMPv4
func TestNat64_ICMP_v6_to_v4_Echo(t *testing.T) {

	eth, ip6, ip4 := testingLayers()

	// Create ICMPv6 Echo Request
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	icmp6Echo := layers.ICMPv6Echo{
		Identifier: 17,
		SeqNumber:  37,
	}

	// Create test packet with payload
	payload := []byte("PING TEST PAYLOAD 1234567890")
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializeLayers(buf, opts, &icmp6, &icmp6Echo, gopacket.Payload(payload)))

	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6, &icmp6Echo, gopacket.Payload(payload))
	t.Log("Origin packet", pkt)

	mappings := []mapping{
		{
			xerror.Unwrap(netip.ParseAddr("192.0.2.1")),
			xerror.Unwrap(netip.ParseAddr("2001:db8::1")),
		},
	}
	memCtx := testutils.NewMemoryContext("nat64_test", datasize.MB)
	defer memCtx.Free()

	m := nat64ModuleConfig(mappings, memCtx)
	require.NotNil(t, m, "Failed to create NAT64 config")

	// Process packet
	result := nat64HandlePackets(m, pkt)
	require.NotEmpty(t, result.Output, "No output packets")
	resultPkt := xpacket.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	// Create expected ICMPv4 packet
	icmp4 := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	icmp4.Id = 17
	icmp4.Seq = 37

	eth.EthernetType = layers.EthernetTypeIPv4
	expectedPkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp4, gopacket.Payload(payload))
	t.Log("Expected packet", expectedPkt)

	// Compare result with expected packet
	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
		cmpopts.IgnoreUnexported(
			layers.Ethernet{},
			layers.IPv4{},
			layers.ICMPv4{},
		),
	)
	require.Empty(t, diff, "Packets don't match")

	// Check payload
	require.Equal(t, payload, resultPkt.ApplicationLayer().Payload(), "Payload doesn't match")
}
