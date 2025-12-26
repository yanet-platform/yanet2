package functional

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func TestACL(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Test framework must be initialized")
	pg := NewPacketGenerator()

	// 1. ACL Configuration Tests
	fw.Run("Configure_ACL_module", func(fw *framework.F, t *testing.T) {
		commands := []string{
			// Configure module
			"/mnt/target/release/yanet-cli-acl update --cfg acl0 --rules /mnt/yanet2/acl.yaml",

			// Configure functions
			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:2=acl:acl0,route:route0",

			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "ACL module configuration failed")
	})

	// 2. Basic Allow/Deny Tests
	fw.Run("Test_allow_UDP", func(fw *framework.F, t *testing.T) {
		// allows traffic from whitelisted IP range
		pkt := pg.UDP(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.3.1"),
			150, 600,
			[]byte("allowed udp traffic"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.NoError(t, err)
		require.NotNil(t, out, "allow packet")
	})

	// FAIL
	fw.Run("Test_deny_UDP", func(fw *framework.F, t *testing.T) {
		// blocks traffic from blacklisted IP range
		pkt := pg.UDP(
			net.ParseIP("192.0.2.99"),
			net.ParseIP("192.0.3.1"),
			150, 600,
			[]byte("denied udp traffic"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.Error(t, err, "Packet should be dropped")
	})

	// 3. Protocol-Specific Tests
	fw.Run("Test_allow_TCP_SYN", func(fw *framework.F, t *testing.T) {
		// permits TCP connection establishment
		pkt := pg.TCP(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.3.1"),
			150, 600,
			true, false, false, false, // SYN only
			[]byte("syn packet"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.NoError(t, err)
		require.NotNil(t, out, "allow SYN packet")
	})

	fw.Run("Test_deny_TCP_RST", func(fw *framework.F, t *testing.T) {
		// protects against TCP reset attacks
		pkt := pg.TCP(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.3.1"),
			150, 600,
			false, false, true, false, // RST only
			[]byte("rst attack"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.Error(t, err, "Packet should be dropped")
	})

	fw.Run("Test_allow_ICMP_Echo", func(fw *framework.F, t *testing.T) {
		pkt := pg.ICMP(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.3.1"),
			layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
			[]byte("ping request"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.NoError(t, err)
		require.NotNil(t, out, "allow ICMP Echo")
	})

	// 4. Complex Rule Validation
	fw.Run("Test_port_range_rules_UDP", func(fw *framework.F, t *testing.T) {
		tests := []struct {
			name     string
			srcPort  uint16
			dstPort  uint16
			expected bool
			desc     string
		}{
			{
				"Allow low port",
				150, 600,
				true,
				"Port at lower end of allowed range (150-450)",
			},
			{
				"Allow mid port",
				300, 600,
				true,
				"Port in middle of allowed range (150-450)",
			},
			{
				"Deny high port",
				700, 700,
				false,
				"Port outside allowed range (150-450)",
			},
		}

		for _, tt := range tests {
			fw.Run(tt.name, func(fw *framework.F, t *testing.T) {
				t.Log(tt.desc)
				pkt := pg.UDP(
					net.ParseIP("192.0.2.2"),
					net.ParseIP("192.0.3.1"),
					tt.srcPort, tt.dstPort,
					[]byte("port test "+strconv.Itoa(int(tt.srcPort))),
				)

				in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
				t.Log("input", in)
				t.Log("output", out)
				if tt.expected {
					require.NoError(t, err)
					require.NotNil(t, out, "Packet should be allowed")
				} else {
					require.Error(t, err, "Packet should be dropped")
				}
			})
		}
	})

	fw.Run("Test_subnet_rules_UDP", func(fw *framework.F, t *testing.T) {
		tests := []struct {
			name     string
			ip       string
			expected bool
			desc     string
		}{
			{
				"Allow subnet",
				"192.0.2.10",
				true,
				"IP from allowed subnet 192.0.2.0/24",
			},
			// FAIL
			{
				"Deny subnet",
				"192.0.99.1",
				false,
				"IP from denied subnet 192.0.99.0/24",
			},
		}

		for _, tt := range tests {
			fw.Run(tt.name, func(fw *framework.F, t *testing.T) {
				t.Log(tt.desc)
				pkt := pg.UDP(
					net.ParseIP(tt.ip),
					net.ParseIP("192.0.3.1"),
					150, 600,
					[]byte("subnet test "+tt.ip),
				)

				in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
				t.Log("input", in)
				t.Log("output", out)
				if tt.expected {
					require.NoError(t, err)
					require.NotNil(t, out, "Packet should be allowed")
				} else {
					require.Error(t, err, "Packet should be dropped")
				}
			})
		}
	})

	// 5. IPv6 Support
	fw.Run("Test_allow_TCPv6", func(fw *framework.F, t *testing.T) {
		pkt := pg.TCPv6(
			net.ParseIP("2001:db8::1"),
			net.ParseIP("2001:db8::2"),
			150, 600,
			true, false, false, false, // SYN
			[]byte("tcpv6 request"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.NoError(t, err)
		require.NotNil(t, out, "TCPv6 SYN")
	})

	fw.Run("Test_allow_ICMPv6", func(fw *framework.F, t *testing.T) {
		pkt := pg.ICMPv6(
			net.ParseIP("2001:db8::1"),
			net.ParseIP("2001:db8::2"),
			layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
			[]byte("ping6 request"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.NoError(t, err)
		require.NotNil(t, out, "ICMPv6 Echo Request")
	})

	fw.Run("Test_deny_UDPv6", func(fw *framework.F, t *testing.T) {
		pkt := pg.UDPv6(
			net.ParseIP("2001:db8::99"), // Blocked source
			net.ParseIP("2001:db8::2"),
			150, 600,
			[]byte("deny udpv6 traffic"),
		)

		in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
		t.Log("input", in)
		t.Log("output", out)
		require.Error(t, err, "Packet should be dropped")
	})

	// 6. Overlapping Rules
	fw.Run("Test_overlapping_pyramid_rules_UDP", func(fw *framework.F, t *testing.T) {
		// Define test cases for the rule pyramid: allow /31 → deny /28 → allow /24 → deny /16
		tests := []struct {
			name     string
			ip       string
			expected bool // true = allow, false = deny
		}{
			{
				"Allow /31 (192.0.2.0/31)",
				"192.0.2.1",
				true,
			},
			// FAILED
			{
				"Deny /28 (192.0.2.0/28)",
				"192.0.2.15",
				false,
			},
			{
				"Allow /24 (192.0.2.0/24)",
				"192.0.2.100",
				true,
			},
			// FAILED
			{
				"Deny /16 (192.0.0.0/16)",
				"192.0.10.1",
				false,
			},
		}

		for _, tt := range tests {
			fw.Run(tt.name, func(fw *framework.F, t *testing.T) {
				pkt := pg.UDP(
					net.ParseIP(tt.ip),
					net.ParseIP("192.0.33.1"),
					150, 600,
					[]byte("overlap test: "+tt.ip),
				)

				in, out, err := fw.SendPacketAndParse(0, 0, pkt, 100*time.Millisecond)
				t.Log("input", in)
				t.Log("output", out)
				if tt.expected {
					require.NoError(t, err)
					require.NotNil(t, out, "Packet should be allowed")
				} else {
					require.Error(t, err, "Packet should be dropped")
				}
			})
		}
	})
}

// Packet generators for different protocols
type PacketGenerator struct {
	SrcMAC, DstMAC net.HardwareAddr
}

func NewPacketGenerator() *PacketGenerator {
	return &PacketGenerator{
		SrcMAC: framework.MustParseMAC(framework.SrcMAC),
		DstMAC: framework.MustParseMAC(framework.DstMAC),
	}
}

func (pg *PacketGenerator) TCP(
	srcIP, dstIP net.IP,
	srcPort, dstPort uint16,
	syn, ack, rst, fin bool,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		SYN:     syn,
		ACK:     ack,
		RST:     rst,
		FIN:     fin,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	return pg.serialize(eth, ip, tcp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) TCPv6(
	srcIP, dstIP net.IP,
	srcPort, dstPort uint16,
	syn, ack, rst, fin bool,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolTCP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		SYN:     syn,
		ACK:     ack,
		RST:     rst,
		FIN:     fin,
	}
	tcp.SetNetworkLayerForChecksum(ip6)

	return pg.serialize(eth, ip6, tcp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) UDP(
	srcIP, dstIP net.IP,
	srcPort, dstPort uint16,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)

	return pg.serialize(eth, ip, udp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) UDPv6(
	srcIP, dstIP net.IP,
	srcPort, dstPort uint16,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}

	ipv6 := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ipv6)

	return pg.serialize(eth, ipv6, udp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) ICMP(
	srcIP, dstIP net.IP,
	icmpType layers.ICMPv4TypeCode,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	icmp := &layers.ICMPv4{
		TypeCode: icmpType,
	}

	return pg.serialize(eth, ip, icmp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) ICMPv6(
	srcIP, dstIP net.IP,
	icmpType layers.ICMPv6TypeCode,
	payload []byte,
) []byte {
	eth := &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	icmp := &layers.ICMPv6{
		TypeCode: icmpType,
	}
	icmp.SetNetworkLayerForChecksum(ip6)

	return pg.serialize(eth, ip6, icmp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) serialize(layers ...gopacket.SerializableLayer) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	if err := gopacket.SerializeLayers(buf, opts, layers...); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
