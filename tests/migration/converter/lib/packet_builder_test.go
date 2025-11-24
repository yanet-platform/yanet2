package lib

import (
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func TestNewPacket_SimpleEtherIPTCP(t *testing.T) {
	pkt, err := NewPacket(nil,
		Ether(
			EtherDst("00:11:22:33:44:55"),
			EtherSrc("00:00:00:00:00:01"),
		),
		IPv4(
			IPSrc("1.2.3.4"),
			IPDst("5.6.7.8"),
			IPTTL(64),
		),
		TCP(
			TCPSport(1234),
			TCPDport(80),
		),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Verify layers
	ethLayer := pkt.Layer(layers.LayerTypeEthernet)
	require.NotNil(t, ethLayer)

	ipLayer := pkt.Layer(layers.LayerTypeIPv4)
	require.NotNil(t, ipLayer)

	tcpLayer := pkt.Layer(layers.LayerTypeTCP)
	require.NotNil(t, tcpLayer)

	// Verify Ethernet fields
	eth := ethLayer.(*layers.Ethernet)
	require.Equal(t, "00:11:22:33:44:55", eth.DstMAC.String())
	require.Equal(t, "00:00:00:00:00:01", eth.SrcMAC.String())

	// Verify IP fields
	ip := ipLayer.(*layers.IPv4)
	require.Equal(t, "1.2.3.4", ip.SrcIP.String())
	require.Equal(t, "5.6.7.8", ip.DstIP.String())
	require.Equal(t, uint8(64), ip.TTL)

	// Verify TCP fields
	tcp := tcpLayer.(*layers.TCP)
	require.Equal(t, layers.TCPPort(1234), tcp.SrcPort)
	require.Equal(t, layers.TCPPort(80), tcp.DstPort)
}

func TestNewPacket_IPv6WithVLAN(t *testing.T) {
	pkt, err := NewPacket(nil,
		Ether(
			EtherDst("00:11:22:33:44:55"),
			EtherSrc("00:00:00:00:00:01"),
		),
		Dot1Q(
			VLANId(100),
		),
		IPv6(
			IPv6Src("::1"),
			IPv6Dst("::2"),
			IPv6HopLimit(64),
		),
		UDP(
			UDPSport(5000),
			UDPDport(5001),
		),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Verify VLAN
	vlanLayer := pkt.Layer(layers.LayerTypeDot1Q)
	require.NotNil(t, vlanLayer)
	vlan := vlanLayer.(*layers.Dot1Q)
	require.Equal(t, uint16(100), vlan.VLANIdentifier)

	// Verify IPv6
	ipv6Layer := pkt.Layer(layers.LayerTypeIPv6)
	require.NotNil(t, ipv6Layer)
	ipv6 := ipv6Layer.(*layers.IPv6)
	require.Equal(t, "::1", ipv6.SrcIP.String())
	require.Equal(t, "::2", ipv6.DstIP.String())
	require.Equal(t, uint8(64), ipv6.HopLimit)

	// Verify UDP
	udpLayer := pkt.Layer(layers.LayerTypeUDP)
	require.NotNil(t, udpLayer)
	udp := udpLayer.(*layers.UDP)
	require.Equal(t, layers.UDPPort(5000), udp.SrcPort)
	require.Equal(t, layers.UDPPort(5001), udp.DstPort)
}

func TestTCPFlags(t *testing.T) {
	tests := []struct {
		name        string
		flags       string
		expectedSYN bool
		expectedACK bool
		expectedFIN bool
		expectedRST bool
		expectedPSH bool
		expectedURG bool
	}{
		{"SYN", "S", true, false, false, false, false, false},
		{"ACK", "A", false, true, false, false, false, false},
		{"SYN-ACK", "SA", true, true, false, false, false, false},
		{"FIN", "F", false, false, true, false, false, false},
		{"RST", "R", false, false, false, true, false, false},
		{"PSH-ACK", "PA", false, true, false, false, true, false},
		{"FIN-ACK", "FA", false, true, true, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt, err := NewPacket(nil,
				Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
				IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
				TCP(TCPSport(1234), TCPDport(80), TCPFlags(tt.flags)),
			)

			require.NoError(t, err)

			tcpLayer := pkt.Layer(layers.LayerTypeTCP)
			require.NotNil(t, tcpLayer)

			tcp := tcpLayer.(*layers.TCP)
			require.Equal(t, tt.expectedSYN, tcp.SYN)
			require.Equal(t, tt.expectedACK, tcp.ACK)
			require.Equal(t, tt.expectedFIN, tcp.FIN)
			require.Equal(t, tt.expectedRST, tcp.RST)
			require.Equal(t, tt.expectedPSH, tcp.PSH)
			require.Equal(t, tt.expectedURG, tcp.URG)
		})
	}
}

func TestPortRange(t *testing.T) {
	ports := PortRange(1024, 1030)
	require.Len(t, ports, 7)
	require.Equal(t, uint16(1024), ports[0])
	require.Equal(t, uint16(1030), ports[6])

	// Test edge cases
	emptyPorts := PortRange(1030, 1024)
	require.Empty(t, emptyPorts)

	singlePort := PortRange(80, 80)
	require.Len(t, singlePort, 1)
	require.Equal(t, uint16(80), singlePort[0])
}

func TestPayload(t *testing.T) {
	// Test simple repeat
	data := Payload("ABC", 3)
	require.Equal(t, []byte("ABCABCABC"), data)

	// Test longer repeat
	data2 := Payload("XY", 5)
	require.Equal(t, []byte("XYXYXYXYXY"), data2)

	// Test with packet
	pkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
		UDP(UDPSport(1234), UDPDport(80)),
		Raw(Payload("TEST", 10)),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Check payload is in the packet
	appLayer := pkt.ApplicationLayer()
	require.NotNil(t, appLayer)
	require.Contains(t, string(appLayer.Payload()), "TEST")
}

func TestICMP(t *testing.T) {
	// Echo request (type 8)
	pkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
		ICMP(ICMPTypeCode(8, 0), ICMPId(0x1234), ICMPSeq(0x5678)),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	icmpLayer := pkt.Layer(layers.LayerTypeICMPv4)
	require.NotNil(t, icmpLayer)

	icmp := icmpLayer.(*layers.ICMPv4)
	require.Equal(t, uint8(8), icmp.TypeCode.Type())
	require.Equal(t, uint8(0), icmp.TypeCode.Code())
	require.Equal(t, uint16(0x1234), icmp.Id)
	require.Equal(t, uint16(0x5678), icmp.Seq)
}

func TestIPv6Fragment(t *testing.T) {
	pkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv6(IPv6Src("::1"), IPv6Dst("::2")),
		IPv6ExtHdrFragment(
			IPv6FragId(0x12345678),
			IPv6FragOffset(0),
			IPv6FragM(true),
		),
		TCP(TCPSport(1234), TCPDport(80)),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	fragLayer := pkt.Layer(layers.LayerTypeIPv6Fragment)
	require.NotNil(t, fragLayer)

	frag := fragLayer.(*layers.IPv6Fragment)
	require.Equal(t, uint32(0x12345678), frag.Identification)
	require.Equal(t, uint16(0), frag.FragmentOffset)
	require.True(t, frag.MoreFragments)
}

func TestGRE(t *testing.T) {
	pkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv6(
			IPv6Src("::"),
			IPv6Dst("1:2:3:4::abcd"),
			IPv6NextHeader(layers.IPProtocolGRE),
		),
		GRE(
			GREChecksumPresent(true),
			GREKeyPresent(true),
		),
		IPv4(IPSrc("0.0.0.0"), IPDst("1.2.3.0")),
		ICMP(ICMPTypeCode(8, 0)),
	)

	require.NoError(t, err)
	require.NotNil(t, pkt)

	greLayer := pkt.Layer(layers.LayerTypeGRE)
	require.NotNil(t, greLayer)

	gre := greLayer.(*layers.GRE)
	require.True(t, gre.ChecksumPresent)
	require.True(t, gre.KeyPresent)
}

// Benchmark tests
func BenchmarkNewPacket_Simple(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = NewPacket(nil,
			Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
			IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
			TCP(TCPSport(1234), TCPDport(80)),
		)
	}
}

func BenchmarkNewPacket_Complex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = NewPacket(nil,
			Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
			Dot1Q(VLANId(100)),
			IPv6(IPv6Src("::1"), IPv6Dst("::2"), IPv6HopLimit(64)),
			IPv6ExtHdrFragment(IPv6FragId(0x12345678), IPv6FragOffset(0), IPv6FragM(true)),
			TCP(TCPSport(1234), TCPDport(80), TCPFlags("SA")),
			Raw(Payload("DATA", 100)),
		)
	}
}
