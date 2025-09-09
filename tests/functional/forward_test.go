package functional

import (
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// createForwardPacket creates a simple TCP packet for forwarding testing
func createForwardPacket(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}
	err := tcp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createICMPPacket creates a simple ICMP echo request packet for testing
func createICMPPacket(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip4, &icmp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createICMPv6Packet creates a simple ICMPv6 echo request packet for testing
func createICMPv6Packet(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	err := icmp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip6, &icmp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// TestForward_BasicFunctionality tests basic forward module functionality
func TestForward(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_Forward_Module", func(t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:4=forward:forward0,route:route0 --instance=0",
			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test --instance=0",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	t.Run("Test_Forwarding", func(t *testing.T) {
		packet := createForwardPacket(
			net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
			net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
			[]byte("forward test"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify packet was forwarded with preserved addresses
		assert.Equal(t, "192.0.2.1", outputPacket.SrcIP.String(), "Source IP should be preserved")
		assert.Equal(t, "192.0.2.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
	})

	t.Run("Test_ICMP4_Echo", func(t *testing.T) {
		packet := createICMPPacket(
			net.ParseIP("203.0.113.1"),
			net.ParseIP("203.0.113.14"),
			[]byte("icmp test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, "203.0.113.14", outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, "203.0.113.1", outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})

	t.Run("Test_ICMP6_Echo", func(t *testing.T) {
		// Test ICMPv6 echo request to fe80::5054:ff:fe6b:ffa5
		packet := createICMPv6Packet(
			net.ParseIP("fe80::1"),                 // src IP
			net.ParseIP("fe80::5054:ff:fe6b:ffa5"), // dst IP (in L3 forwarding table)
			[]byte("icmpv6 test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMPv6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify that we received an ICMPv6 echo reply
		// For ICMPv6 echo reply, the type should be 129 (EchoReply)
		// and the addresses should be swapped
		assert.Equal(t, "fe80::5054:ff:fe6b:ffa5", outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, "fe80::1", outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})
}
