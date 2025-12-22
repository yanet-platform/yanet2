package functional

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// setAndWaitForNAT64DropFlags sets NAT64 drop flags and waits for them to be applied
func setAndWaitForNAT64DropFlags(fw *framework.TestFramework, dropUnknownPrefix, dropUnknownMapping bool, timeout time.Duration) error {
	// Build the drop command
	cmd := "/mnt/target/release/yanet-cli-nat64 drop --cfg nat64_0"
	if dropUnknownPrefix {
		cmd += " --drop-unknown-prefix"
	}
	if dropUnknownMapping {
		cmd += " --drop-unknown-mapping"
	}

	// Execute the drop command
	_, err := fw.CLI.ExecuteCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to set NAT64 drop flags: %w", err)
	}

	// Wait for flags to be applied
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-nat64 show --cfg nat64_0 --format json")
		if err != nil {
			return fmt.Errorf("failed to check NAT64 status: %w", err)
		}

		status := struct {
			Config struct {
				DropUnknownPrefix  bool `json:"drop_unknown_prefix"`
				DropUnknownMapping bool `json:"drop_unknown_mapping"`
			}
		}{}
		err = json.Unmarshal([]byte(output), &status)
		if err != nil {
			return fmt.Errorf("failed to parse NAT64 status ===%s===: %w", output, err)
		}
		// Check if flags match expected state
		if (dropUnknownPrefix == status.Config.DropUnknownPrefix) && (dropUnknownMapping == status.Config.DropUnknownMapping) {
			return nil
		}

		// Wait before next check
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for NAT64 drop flags to be applied (prefix=%v, mapping=%v)", dropUnknownPrefix, dropUnknownMapping)
}

// TestNAT64_BasicFunctionality tests basic NAT64 module functionality
func TestNAT64(t *testing.T) {
	// Use global framework instance like in TestYANETStartup
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_NAT64_Module", func(t *testing.T) {

		// NAT64-specific configuration
		commands := []string{
			// Configure NAT64 mappings (using addresses from unit tests)
			"/mnt/target/release/yanet-cli-nat64 prefix add --cfg nat64_0 --prefix 2001:db8::/96",
			"/mnt/target/release/yanet-cli-nat64 mapping add --cfg nat64_0 --ipv4 198.51.100.1 --ipv6 2001:db8::4 --prefix-index 0",
			"/mnt/target/release/yanet-cli-nat64 mapping add --cfg nat64_0 --ipv4 198.51.100.2 --ipv6 2001:db8::3 --prefix-index 0",

			"/mnt/target/release/yanet-cli-function update --name=test --chains chain0:1=forward:forward0,nat64:nat64_0,route:route0",
			// Configure pipeline
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	t.Run("Test_IPv4_to_IPv6_Translation", func(t *testing.T) {
		// From outer_ip4 (192.0.2.34) to mapped address (198.51.100.2)
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.34"),   // outer_ip4 from unit tests -> embedded as 2001:db8::c000:222
			net.ParseIP("198.51.100.2"), // mapped IPv4 -> 2001:db8::3
			createTCPLayer(),
			[]byte("ipv4 to ipv6"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv4 to IPv6 translation
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(80), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv6_to_IPv4_Translation", func(t *testing.T) {
		// From mapped IPv6 (2001:db8::3) to embedded IPv6 (2001:db8::c000:222)
		packet := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("ipv6 to ipv4"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, outputPacket.IsIPv4, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(80), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv4_to_IPv6_Translation_UDP", func(t *testing.T) {
		// Test IPv4 to IPv6 translation using UDP packets
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.34"),
			net.ParseIP("198.51.100.2"),
			createUDPLayer(),
			createDNSPayload(),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 UDP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(53), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv6_to_IPv4_Translation_UDP", func(t *testing.T) {
		// Test IPv6 to IPv4 translation - reverse direction with UDP packets
		packet := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createUDPLayer(),
			createDNSPayload(),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 UDP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv6 to IPv4 translation
		require.True(t, outputPacket.IsIPv4, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(53), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv4_to_IPv6_Translation_ICMP", func(t *testing.T) {
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.34"),
			net.ParseIP("198.51.100.2"),
			createICMPv4Layer(),
			[]byte("ipv4 to ipv6 icmp"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		require.Equal(t, layers.IPProtocolICMPv6, outputPacket.NextHeader, "Protocol should be translated to ICMPv6")
	})

	t.Run("Test_IPv6_to_IPv4_Translation_ICMP", func(t *testing.T) {
		packet := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createICMPv6Layer(),
			[]byte("ipv6 to ipv4 icmp"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, outputPacket.IsIPv4, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		require.Equal(t, layers.IPProtocolICMPv4, outputPacket.Protocol, "Protocol should be translated to ICMPv4")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixTrue_MappingTrue", func(t *testing.T) {
		// Set drop-unknown-prefix=true, drop-unknown-mapping=true
		err := setAndWaitForNAT64DropFlags(fw, true, true, 10*time.Second)
		require.NoError(t, err, "Failed to set and wait for NAT64 drop flags")

		// Test IPv6 packet with known prefix and mapping - should be translated
		ipv6PacketKnown := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // known mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("known mapping"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6PacketKnown, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		require.True(t, outputPacket.IsIPv4, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		ipv4PacketKnown := createNAT64Packet(
			net.ParseIP("192.0.2.34"),
			net.ParseIP("198.51.100.2"),
			createTCPLayer(),
			[]byte("known mapping"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv4PacketKnown, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		// Test IPv6 packet with unknown prefix - should be dropped
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv6Packet, 500*time.Millisecond)
		require.Error(t, err, "Packet should be dropped")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")

		// Test IPv4 packet with unknown mapping - should be dropped
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv4Packet, 500*time.Millisecond)
		require.Error(t, err, "Packet should be dropped")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixTrue_MappingFalse", func(t *testing.T) {
		// Set drop-unknown-prefix=true, drop-unknown-mapping=false
		err := setAndWaitForNAT64DropFlags(fw, true, false, 10*time.Second)
		require.NoError(t, err, "Failed to set and wait for NAT64 drop flags")

		// Test IPv6 packet with unknown prefix - should be dropped
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.Error(t, err, "Packet should be dropped")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")

		// Test IPv4 packet with unknown mapping - should be passed through
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		require.True(t, outputPacket.IsIPv4, "Output packet should remain IPv4")
		assert.Equal(t, "192.0.2.100", outputPacket.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "192.0.2.101", outputPacket.DstIP.String(), "Destination should remain unchanged")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixFalse_MappingTrue", func(t *testing.T) {
		// Set drop-unknown-prefix=false, drop-unknown-mapping=true
		err := setAndWaitForNAT64DropFlags(fw, false, true, 10*time.Second)
		require.NoError(t, err, "Failed to set and wait for NAT64 drop flags")

		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.Error(t, err, "Packet should be dropped")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Output packet should be present")

		// Test IPv4 packet with unknown mapping - should be dropped
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.Error(t, err, "Packet should be dropped")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixFalse_MappingFalse", func(t *testing.T) {
		// Set both drop flags to false
		err := setAndWaitForNAT64DropFlags(fw, false, false, 10*time.Second)
		require.NoError(t, err, "Failed to set and wait for NAT64 drop flags")

		// Test IPv6 packet with unknown prefix - should be passed through
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		require.True(t, inputPacket.IsIPv6, "Input packet should be IPv6")
		require.True(t, outputPacket.IsIPv6, "Output packet should remain IPv6")
		assert.Equal(t, "2001:db9::3", outputPacket.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "2001:db9::c000:222", outputPacket.DstIP.String(), "Destination should remain unchanged")

		// Test IPv4 packet with unknown mapping - should be passed through
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		require.True(t, inputPacket.IsIPv4, "Input packet should be IPv4")
		require.True(t, outputPacket.IsIPv4, "Output packet should remain IPv4")
		assert.Equal(t, "192.0.2.100", outputPacket.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "192.0.2.101", outputPacket.DstIP.String(), "Destination should remain unchanged")
	})
}

// Helper function to create TCP layer
func createTCPLayer() *layers.TCP {
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(12345),
		DstPort: layers.TCPPort(80),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}
	return tcp
}

// Helper function to create UDP layer
func createUDPLayer() *layers.UDP {
	udp := &layers.UDP{
		SrcPort: layers.UDPPort(12345),
		DstPort: layers.UDPPort(53), // DNS
	}
	return udp
}

// Helper function to create DNS payload
func createDNSPayload() []byte {
	return []byte{
		// DNS header (12 bytes)
		0x12, 0x34, // ID
		0x01, 0x00, // Flags (standard query)
		0x00, 0x01, // Questions
		0x00, 0x00, // Answer RRs
		0x00, 0x00, // Authority RRs
		0x00, 0x00, // Additional RRs
		// Question section
		// Name: "example.com" in DNS format
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,       // End of name
		0x00, 0x01, // Type A
		0x00, 0x01, // Class IN
	}
}

// Helper function to create ICMPv4 layer
func createICMPv4Layer() *layers.ICMPv4 {
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       0x1234,
		Seq:      1,
	}
	return icmp
}

// Helper function to create ICMPv6 layer
func createICMPv6Layer() *layers.ICMPv6 {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	return icmp
}

// Helper function to create NAT64 test packets
func createNAT64Packet(srcIP, dstIP net.IP, l4 gopacket.SerializableLayer, payload []byte) []byte {
	// Determine if IPv4 or IPv6 based on IP addresses
	var ethType layers.EthernetType
	var ipLayer gopacket.SerializableLayer

	if srcIP.To4() != nil && dstIP.To4() != nil {
		// IPv4
		ethType = layers.EthernetTypeIPv4
		ip4 := &layers.IPv4{
			Version: 4,
			IHL:     5,
			Id:      1,
			TTL:     64,
			SrcIP:   srcIP,
			DstIP:   dstIP,
		}

		// Set protocol based on layer 4 type
		switch l4.(type) {
		case *layers.TCP:
			ip4.Protocol = layers.IPProtocolTCP
		case *layers.UDP:
			ip4.Protocol = layers.IPProtocolUDP
		case *layers.ICMPv4:
			ip4.Protocol = layers.IPProtocolICMPv4
		}

		ipLayer = ip4
	} else {
		// IPv6
		ethType = layers.EthernetTypeIPv6
		ip6 := &layers.IPv6{
			Version:  6,
			HopLimit: 64,
			SrcIP:    srcIP,
			DstIP:    dstIP,
		}

		// Set next header based on layer 4 type
		switch l4.(type) {
		case *layers.TCP:
			ip6.NextHeader = layers.IPProtocolTCP
		case *layers.UDP:
			ip6.NextHeader = layers.IPProtocolUDP
		case *layers.ICMPv6:
			ip6.NextHeader = layers.IPProtocolICMPv6
		}

		ipLayer = ip6
	}

	// Create Ethernet layer
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: ethType,
	}

	// Set checksum for layer 4 if needed
	if tcp, ok := l4.(*layers.TCP); ok {
		if ethType == layers.EthernetTypeIPv4 {
			ip4 := ipLayer.(*layers.IPv4)
			tcp.SetNetworkLayerForChecksum(ip4)
		} else {
			ip6 := ipLayer.(*layers.IPv6)
			tcp.SetNetworkLayerForChecksum(ip6)
		}
	} else if udp, ok := l4.(*layers.UDP); ok {
		if ethType == layers.EthernetTypeIPv4 {
			ip4 := ipLayer.(*layers.IPv4)
			udp.SetNetworkLayerForChecksum(ip4)
		} else {
			ip6 := ipLayer.(*layers.IPv6)
			udp.SetNetworkLayerForChecksum(ip6)
		}
	} else if icmp6, ok := l4.(*layers.ICMPv6); ok {
		ip6 := ipLayer.(*layers.IPv6)
		icmp6.SetNetworkLayerForChecksum(ip6)
	}

	// Serialize layers
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, ipLayer, l4, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}
