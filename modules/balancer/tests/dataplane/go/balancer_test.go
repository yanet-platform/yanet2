package balancer_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

func createUDPPacket(srcIP string, dstIP string) []gopacket.SerializableLayer {

	src := net.ParseIP(srcIP)
	dst := net.ParseIP(dstIP)

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    src,
			DstIP:    dst,
		}
	} else {
		ip = &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolUDP,
			HopLimit:   64,
			SrcIP:      src,
			DstIP:      dst,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       common.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       common.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: ethernetType,
	}

	udp := &layers.UDP{
		SrcPort: 1234,
		DstPort: 5678,
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	return []gopacket.SerializableLayer{eth, ip.(gopacket.SerializableLayer), udp, gopacket.Payload(payload)}
}

func encapsulate(t *testing.T, origLayers []gopacket.SerializableLayer, srcIP string, dstIP string) gopacket.Packet {
	ipv4 := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolIPv4,
		SrcIP:    net.ParseIP(srcIP),
		DstIP:    net.ParseIP(dstIP),
	}
	newLayers := make([]gopacket.SerializableLayer, 0, len(origLayers)+1)
	newLayers = append(newLayers, origLayers[0], ipv4)
	newLayers = append(newLayers, origLayers[1:]...)
	return common.LayersToPacket(t, newLayers...)
}

func TestBalancer_HappyPath_IPV4(t *testing.T) {
	m := balancerModuleConfig()
	require.NotNil(t, m, "Failed to create balancer config")

	balancerModuleConfigAddService(m, balancerServiceConfig{
		addr: common.Unwrap(netip.ParseAddr("192.0.0.2")),
		reals: []balancerRealConfig{
			{
				dst: common.Unwrap(netip.ParseAddr("192.0.0.3")),
				src: common.Unwrap(netip.ParseAddr("192.1.0.3")),
			},
		},
		prefixes: []netip.Prefix{common.Unwrap(netip.ParsePrefix("192.0.0.0/24"))},
	})

	inLayers := createUDPPacket("192.0.0.1", "192.0.0.2")
	pkt := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", pkt)

	expectedPkt := encapsulate(t, inLayers, "192.1.0.3", "192.0.0.3")
	t.Log("Expected packet", expectedPkt)

	// Process packet
	result := balancerHandlePackets(m, pkt)
	require.NotEmpty(t, result.Output, "No output packets")
	resultPkt := common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPkt)

	// Compare result with expected packet
	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
		cmpopts.IgnoreUnexported(
			layers.Ethernet{},
			layers.IPv4{},
			layers.UDP{},
		),
	)
	require.Empty(t, diff, "Packets don't match")

	// Check payload
	require.Equal(t, expectedPkt.ApplicationLayer().Payload(),
		resultPkt.ApplicationLayer().Payload(), "Payload doesn't match")
}

func TestBalancer_SrcCheck(t *testing.T) {
	m := balancerModuleConfig()
	require.NotNil(t, m, "Failed to create balancer config")

	balancerModuleConfigAddService(m, balancerServiceConfig{
		addr: common.Unwrap(netip.ParseAddr("192.0.0.2")),
		reals: []balancerRealConfig{
			{
				dst: common.Unwrap(netip.ParseAddr("192.0.0.3")),
				src: common.Unwrap(netip.ParseAddr("192.1.0.3")),
			},
		},
		prefixes: []netip.Prefix{
			common.Unwrap(netip.ParsePrefix("192.0.0.0/24")),
		},
	})

	balancerModuleConfigAddService(m, balancerServiceConfig{
		addr: common.Unwrap(netip.ParseAddr("1:2:3:4::")),
		reals: []balancerRealConfig{
			{
				dst: common.Unwrap(netip.ParseAddr("192.0.0.3")),
				src: common.Unwrap(netip.ParseAddr("192.1.0.3")),
			},
		},
		prefixes: []netip.Prefix{
			common.Unwrap(netip.ParsePrefix("1:2:3:4::/64")),
		},
	})

	type testcase struct {
		src, dst string
		ok       bool
		name     string
	}

	for _, tc := range []testcase{
		{"192.0.0.1", "192.0.0.2", true, "ipv4 balance"},
		{"192.1.0.0", "192.0.0.2", false, "ipv4 drop"},
		{"1:2:3:4:5::", "1:2:3:4::", true, "ipv6 balance"},
		{"1::", "1:2:3:4::", false, "ipv6 drop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inLayers := createUDPPacket(tc.src, tc.dst)

			pkt := common.LayersToPacket(t, inLayers...)
			t.Log("Origin packet", pkt)

			result := balancerHandlePackets(m, pkt)
			if tc.ok {
				assert.NotEmpty(t, result.Output, "No output packets")
			} else {
				assert.Empty(t, result.Output, "Packet should be dropped")
			}
		})
	}

}
