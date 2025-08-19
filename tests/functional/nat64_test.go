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

// TestNAT64_BasicFunctionality tests basic NAT64 module functionality
func TestNAT64(t *testing.T) {
	// Use global framework instance like in TestYANETStartup
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_NAT64_Module", func(t *testing.T) {
		// Configure forward module first (L2 and L3 forwarding)
		commands := []string{
			"ip link set kni0 up",
			"ip nei add fe80::1 lladdr " + framework.SrcMAC + " dev kni0",
			"ip nei add 203.0.113.1 lladdr " + framework.SrcMAC + " dev kni0",
			"ip addr add 203.0.113.14/24 dev kni0",
			"sleep 3",
			// Enable L2 forwarding between devices
			"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 0 --dst 1",
			"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 1 --dst 0",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net 203.0.113.14/32",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net fe80::5054:ff:fe6b:ffa5/64",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net ff02::/16",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net 0.0.0.0/0",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net ::/0",

			// Route
			"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via fe80::1 ::/0",
			"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via 203.0.113.1 0.0.0.0/0",

			// Configure NAT64 mappings (using addresses from unit tests)
			"/mnt/target/release/yanet-cli-nat64 prefix add --cfg nat64_0 --instances 0 --prefix 2001:db8::/96",
			"/mnt/target/release/yanet-cli-nat64 mapping add --cfg nat64_0 --instances 0 --ipv4 198.51.100.1 --ipv6 2001:db8::4 --prefix-index 0",
			"/mnt/target/release/yanet-cli-nat64 mapping add --cfg nat64_0 --instances 0 --ipv4 198.51.100.2 --ipv6 2001:db8::3 --prefix-index 0",
			"/mnt/target/release/yanet-cli-nat64 show --cfg nat64_0 --instances 0",

			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=bootstrap --modules forward:forward0 --instance=0",
			"/mnt/target/release/yanet-cli-pipeline update --name=nat64 --modules forward:forward0 --modules nat64:nat64_0 --modules route:route0 --instance=0",

			// Assign pipelines to devices
			"/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=01:00.0 --pipelines nat64:1",
			"/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=virtio_user_kni0 --pipelines bootstrap:1",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			if output != "" {
				t.Logf("Output: %s", output)
			}
		}
	})

	t.Run("Test_IPv4_to_IPv6_Translation", func(t *testing.T) {
		// Test IPv4 to IPv6 translation using correct addresses from unit tests
		// From outer_ip4 (192.0.2.34) to mapped address (198.51.100.2)
		packet := createNAT64IPv4Packet(
			net.ParseIP("192.0.2.34"),   // outer_ip4 from unit tests -> embedded as 2001:db8::c000:222
			net.ParseIP("198.51.100.2"), // mapped IPv4 -> 2001:db8::3
			[]byte("ipv4 to ipv6"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv4 to IPv6 translation
		require.False(t, inputPacket.IsIPv6, "Input packet should be IPv4")
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		// 192.0.2.34 (0xc000222) embedded in NAT64 prefix becomes 2001:db8::c000:222
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")
	})

	t.Run("Test_IPv6_to_IPv4_Translation", func(t *testing.T) {
		// Test IPv6 to IPv4 translation - reverse direction
		// From mapped IPv6 (2001:db8::3) to embedded IPv6 (2001:db8::c000:222)
		packet := createNAT64IPv6Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			[]byte("ipv6 to ipv4"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv6 to IPv4 translation
		require.True(t, inputPacket.IsIPv6, "Input packet should be IPv6")
		require.False(t, outputPacket.IsIPv6, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")
	})

	t.Run("Test_Non_Mapped_Address", func(t *testing.T) {
		// Test packet with address not in NAT64 mapping
		packet := createNAT64IPv4Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			[]byte("no mapping"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")

		// Non-mapped packets pass through unchanged (NAT64 pass-through mode)
		require.False(t, inputPacket.IsIPv6, "Input packet should be IPv4")
		require.False(t, outputPacket.IsIPv6, "Output packet should remain IPv4")
		assert.Equal(t, "192.0.2.100", outputPacket.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "192.0.2.101", outputPacket.DstIP.String(), "Destination should remain unchanged")
	})
}

// Helper function to create NAT64 IPv4 test packets
func createNAT64IPv4Packet(srcIP, dstIP net.IP, payload []byte) []byte {
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

// Helper function to create NAT64 IPv6 test packets
func createNAT64IPv6Packet(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolTCP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
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
	err := tcp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip6, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}
