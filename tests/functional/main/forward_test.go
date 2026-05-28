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

// TestForward tests basic forward module functionality including L2 forwarding
// and ICMP echo through the kni0 kernel interface.
func TestForward(t *testing.T) {
	withBootedVM(t, func(fw *framework.TestFramework) {
		testForward(t, fw)
	})
}

func testForward(t *testing.T, fw *framework.TestFramework) {
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Configure_Forward_Module", func(fw *framework.TestFramework, t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=forward:forward0,route:route0",
			// Configure pipelines
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	fw.Run("Test_Forwarding", func(fw *framework.TestFramework, t *testing.T) {
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
			net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
			[]byte("forward test"),
			nil,
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify packet was forwarded with preserved addresses
		assert.Equal(t, "192.0.2.1", outputPacket.SrcIP.String(), "Source IP should be preserved")
		assert.Equal(t, "192.0.2.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
	})

	fw.Run("Test_ICMP4_Echo", func(fw *framework.TestFramework, t *testing.T) {
		packet := createICMPPacket(
			net.ParseIP(framework.VMIPv4Gateway),
			net.ParseIP(framework.VMIPv4Host),
			[]byte("icmp test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, framework.VMIPv4Host, outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, framework.VMIPv4Gateway, outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})

	fw.Run("Test_ICMP6_Echo", func(fw *framework.TestFramework, t *testing.T) {
		// Test ICMPv6 echo request to VMIPv6Host
		packet := createICMPv6Packet(
			net.ParseIP(framework.VMIPv6Gateway), // src IP
			net.ParseIP(framework.VMIPv6Host),    // dst IP (in L3 forwarding table)
			[]byte("icmpv6 test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send ICMPv6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify that we received an ICMPv6 echo reply
		// For ICMPv6 echo reply, the type should be 129 (EchoReply)
		// and the addresses should be swapped
		assert.Equal(t, framework.VMIPv6Host, outputPacket.SrcIP.String(), "Source IP should be the destination of the request")
		assert.Equal(t, framework.VMIPv6Gateway, outputPacket.DstIP.String(), "Destination IP should be the source of the request")
	})

	fw.Run("Test_Batch", func(fw *framework.TestFramework, t *testing.T) {
		// Send two packets, the first one is passed through forward
		// module whereas the second should be routed to a kernel and
		// responded with an ICMP
		packets := [][]byte{
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
			createICMPPacket(
				net.ParseIP(framework.VMIPv4Gateway),
				net.ParseIP(framework.VMIPv4Host),
				[]byte("icmp test"),
			),
			framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
				net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
				[]byte("forward test"),
				nil,
			),
		}

		outputPackets, err := fw.SendPacketsAndParseAll(0, 0, packets, 500*time.Millisecond)
		require.NoError(t, err, "Failed to send batch")

		assert.Equal(t, 8, len(outputPackets), "eight packets expected")
		cntICMP := 0
		for _, pack := range outputPackets {
			if framework.VMIPv4Host == pack.SrcIP.String() &&
				framework.VMIPv4Gateway == pack.DstIP.String() {
				cntICMP++
			}
		}

		assert.Equal(t, 4, cntICMP, "four ICMP replies are expected")
	})

}
