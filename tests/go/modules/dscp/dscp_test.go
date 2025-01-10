package dscp_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"tests/common"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func mark(t *testing.T, pkt gopacket.Packet, dscp uint8) gopacket.Packet {
	network := pkt.NetworkLayer()
	tc := dscp << 2 // ECN in the first two bits
	switch network.LayerType() {
	case layers.LayerTypeIPv4:
		network.(*layers.IPv4).TOS = tc
	case layers.LayerTypeIPv6:
		network.(*layers.IPv6).TrafficClass = tc
	default:
		t.Logf("unexpected network layer type: %v", network.LayerType())
		t.FailNow()
	}
	// Serialize and then Deserialize the packet so, the gopacket will recalculate checksum for us.
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializePacket(buf, opts, pkt))

	pkt = gopacket.NewPacket(
		buf.Bytes(),
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
	require.Empty(t, pkt.ErrorLayer())
	return pkt
}

func TestDSCP(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       common.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       common.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	eth4 := eth
	eth4.EthernetType = layers.EthernetTypeIPv4
	eth6 := eth
	eth6.EthernetType = layers.EthernetTypeIPv6

	vlan4 := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv4,
	}
	vlan6 := vlan4
	vlan6.Type = layers.EthernetTypeIPv6

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv4,
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

	prefixes := []netip.Prefix{
		common.Unwrap(netip.ParsePrefix("1:2:3:4::/64")),
		common.Unwrap(netip.ParsePrefix("1.1.0.0/32")),
	}

	testData := []struct {
		name string
		pkt  gopacket.Packet
	}{
		{"v4", common.LayersToPacket(t, &eth4, &ip4, &icmp)},
		{"v6", common.LayersToPacket(t, &eth6, &ip6, &icmp)},
		{"vlan>v4", common.LayersToPacket(t, &eth, &vlan4, &ip4, &icmp)},
		{"vlan>v6", common.LayersToPacket(t, &eth, &vlan6, &ip6, &icmp)},
	}

	cases := []struct {
		name string
		flag uint8
		mark uint8 // DSCP value in the module config
		from uint8 // original DSCP value of the packet
		expt uint8 // expected DSCP value
	}{
		{"Never remark 0->10=10", DSCPMarkNever, 0, 10, 10},
		{"Never remark 0-> 0= 0", DSCPMarkNever, 0, 0, 0},
		{"Never remark 7->60=60", DSCPMarkNever, 7, 60, 60},
		{"Never remark 7->70=70", DSCPMarkNever, 7, 70, 70},

		{"Always mark 40-> 0=40", DSCPMarkAlways, 40, 0, 40},
		{"Always mark 7 ->16= 7", DSCPMarkAlways, 7, 16, 7},
		{"Always mark 10->26=10", DSCPMarkAlways, 10, 26, 10},
		{"Always mark 0 ->36= 0", DSCPMarkAlways, 0, 36, 0},

		{"Default mark 7 ->13=13", DSCPMarkDefault, 7, 13, 13},
		{"Default mark 7 -> 0= 7", DSCPMarkDefault, 7, 0, 7},
		{"Default mark 0 -> 1= 1", DSCPMarkDefault, 0, 1, 1},
		{"Default mark 0 -> 0= 0", DSCPMarkDefault, 0, 0, 0},
	}

	for _, c := range cases {
		for _, p := range testData {
			t.Run(fmt.Sprintf("pkt=%s: %s", p.name, c.name), func(t *testing.T) {
				pkt := mark(t, p.pkt, c.from)
				t.Log("Origin packet", pkt)

				memCtx := memCtxCreate()
				m := dscpModuleConfig(prefixes, c.flag, c.mark, memCtx)
				result := dscpHandlePackets(m, pkt)
				require.NotEmpty(t, result.Output, "result.Output")

				resultPkt := common.ParseEtherPacket(result.Output[0])
				t.Log("Result packet", resultPkt)

				expectedPkt := mark(t, p.pkt, c.expt)
				t.Log("Expected packet", expectedPkt)

				diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers(),
					cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
				)
				require.Empty(t, diff)
			})
		}
	}

}
