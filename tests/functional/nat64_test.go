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
		require.False(t, inputPacket.IsIPv6, "Input packet should be IPv4")
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		// 192.0.2.34 (0xc000222) embedded in NAT64 prefix becomes 2001:db8::c000:222
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		// Verify TCP ports are preserved
		// Source port 12345 should be preserved
		// Destination port 80 (HTTP) should be preserved
		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(80), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv6_to_IPv4_Translation", func(t *testing.T) {
		// Test IPv6 to IPv4 translation - reverse direction
		// From mapped IPv6 (2001:db8::3) to embedded IPv6 (2001:db8::c000:222)
		packet := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
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

		// Verify TCP ports are preserved
		// Source port 12345 should be preserved
		// Destination port 80 (HTTP) should be preserved
		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(80), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_Non_Mapped_Address", func(t *testing.T) {
		// Test packet with address not in NAT64 mapping
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
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

	t.Run("Test_IPv4_to_IPv6_Translation_UDP", func(t *testing.T) {
		// Test IPv4 to IPv6 translation using UDP packets
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.34"),   // outer_ip4 from unit tests -> embedded as 2001:db8::c000:222
			net.ParseIP("198.51.100.2"), // mapped IPv4 -> 2001:db8::3
			createUDPLayer(),
			createDNSPayload(),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 UDP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv4 to IPv6 translation
		require.False(t, inputPacket.IsIPv6, "Input packet should be IPv4")
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		// 192.0.2.34 (0xc000222) embedded in NAT64 prefix becomes 2001:db8::c000:222
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		// Verify UDP ports are preserved
		// Source port 12345 should be preserved
		// Destination port 53 (DNS) should be preserved
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

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 UDP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv6 to IPv4 translation
		require.True(t, inputPacket.IsIPv6, "Input packet should be IPv6")
		require.False(t, outputPacket.IsIPv6, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		// Verify UDP ports are preserved
		// Source port 12345 should be preserved
		// Destination port 53 (DNS) should be preserved
		assert.Equal(t, uint16(12345), outputPacket.SrcPort, "Source port should be preserved")
		assert.Equal(t, uint16(53), outputPacket.DstPort, "Destination port should be preserved")
	})

	t.Run("Test_IPv4_to_IPv6_Translation_ICMP", func(t *testing.T) {
		// Test IPv4 to IPv6 translation using ICMP packets
		packet := createNAT64Packet(
			net.ParseIP("192.0.2.34"),   // outer_ip4 from unit tests -> embedded as 2001:db8::c000:222
			net.ParseIP("198.51.100.2"), // mapped IPv4 -> 2001:db8::3
			createICMPv4Layer(),
			[]byte("ipv4 to ipv6 icmp"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv4 ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv4 to IPv6 translation
		require.False(t, inputPacket.IsIPv6, "Input packet should be IPv4")
		require.True(t, outputPacket.IsIPv6, "Output packet should be IPv6")
		// 192.0.2.34 (0xc000222) embedded in NAT64 prefix becomes 2001:db8::c000:222
		assert.Equal(t, "2001:db8::c000:222", outputPacket.SrcIP.String(), "Source should be IPv4-embedded in NAT64 prefix")
		assert.Equal(t, "2001:db8::3", outputPacket.DstIP.String(), "Destination should be mapped IPv6")

		// Verify ICMP protocol is translated
		// ICMPv4 should be translated to ICMPv6
		require.Equal(t, layers.IPProtocolICMPv6, outputPacket.NextHeader, "Protocol should be translated to ICMPv6")
	})

	t.Run("Test_IPv6_to_IPv4_Translation_ICMP", func(t *testing.T) {
		// Test IPv6 to IPv4 translation - reverse direction with ICMP packets
		packet := createNAT64Packet(
			net.ParseIP("2001:db8::3"),        // mapped IPv6 -> 198.51.100.2
			net.ParseIP("2001:db8::c000:222"), // embedded IPv6 -> 192.0.2.34
			createICMPv6Layer(),
			[]byte("ipv6 to ipv4 icmp"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send IPv6 ICMP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify IPv6 to IPv4 translation
		require.True(t, inputPacket.IsIPv6, "Input packet should be IPv6")
		require.False(t, outputPacket.IsIPv6, "Output packet should be IPv4")
		assert.Equal(t, "198.51.100.2", outputPacket.SrcIP.String(), "Source should be mapped IPv4")
		assert.Equal(t, "192.0.2.34", outputPacket.DstIP.String(), "Destination should be extracted from NAT64 prefix")

		// Verify ICMP protocol is translated
		// ICMPv6 should be translated to ICMPv4
		require.Equal(t, layers.IPProtocolICMPv4, outputPacket.Protocol, "Protocol should be translated to ICMPv4")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixTrue_MappingTrue", func(t *testing.T) {
		// Configure NAT64 with drop_unknown_prefix=true and drop_unknown_mapping=true
		commands := []string{
			"/mnt/target/release/yanet-cli-nat64 drop --cfg nat64_0 --instances 0 --drop-unknown-prefix --drop-unknown-mapping",
			"/mnt/target/release/yanet-cli-nat64 show --cfg nat64_0 --instances 0",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			if output != "" {
				t.Logf("Command '%s' output: %s", cmd, output)
			}
		}

		// Test IPv6 packet with unknown prefix - should be dropped
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		// With drop_unknown_prefix=true, the packet should be dropped, so outputPacket should be nil
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")

		// Test IPv4 packet with unknown mapping - should be dropped
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		// Send packet and wait for response
		inputPacket2, outputPacket2, err := fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket2, "Input packet should be parsed")
		// With drop_unknown_mapping=true, the packet should be dropped, so outputPacket should be nil
		assert.Nil(t, outputPacket2, "Output packet should be nil (dropped)")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixTrue_MappingFalse", func(t *testing.T) {
		// Configure NAT64 with drop_unknown_prefix=true and drop_unknown_mapping=false
		commands := []string{
			"/mnt/target/release/yanet-cli-nat64 drop --cfg nat64_0 --instances 0 --drop-unknown-prefix",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			if output != "" {
				t.Logf("Output: %s", output)
			}
		}

		// Test IPv6 packet with unknown prefix - should be dropped
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		// With drop_unknown_prefix=true, the packet should be dropped, so outputPacket should be nil
		assert.Nil(t, outputPacket, "Output packet should be nil (dropped)")

		// Test IPv4 packet with unknown mapping - should be passed through
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		// Send packet and wait for response
		inputPacket2, outputPacket2, err := fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket2, "Input packet should be parsed")
		require.NotNil(t, outputPacket2, "Output packet should be present")
		// With drop_unknown_mapping=false, the packet should be passed through unchanged
		require.False(t, inputPacket2.IsIPv6, "Input packet should be IPv4")
		require.False(t, outputPacket2.IsIPv6, "Output packet should remain IPv4")
		assert.Equal(t, "192.0.2.100", outputPacket2.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "192.0.2.101", outputPacket2.DstIP.String(), "Destination should remain unchanged")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixFalse_MappingTrue", func(t *testing.T) {
		// Configure NAT64 with drop_unknown_prefix=false and drop_unknown_mapping=true
		commands := []string{
			"/mnt/target/release/yanet-cli-nat64 drop --cfg nat64_0 --instances 0 --drop-unknown-mapping",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			if output != "" {
				t.Logf("Output: %s", output)
			}
		}

		// Test IPv6 packet with unknown prefix - should be passed through
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		// With drop_unknown_prefix=false, the packet should be passed through unchanged
		require.True(t, inputPacket.IsIPv6, "Input packet should be IPv6")
		require.True(t, outputPacket.IsIPv6, "Output packet should remain IPv6")
		assert.Equal(t, "2001:db9::3", outputPacket.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "2001:db9::c000:222", outputPacket.DstIP.String(), "Destination should remain unchanged")

		// Test IPv4 packet with unknown mapping - should be dropped
		ipv4Packet := createNAT64Packet(
			net.ParseIP("192.0.2.100"), // src IPv4 (not in mapping)
			net.ParseIP("192.0.2.101"), // dst IPv4 (not in mapping)
			createTCPLayer(),
			[]byte("unknown mapping"),
		)

		// Send packet and wait for response
		inputPacket2, outputPacket2, err := fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket2, "Input packet should be parsed")
		// With drop_unknown_mapping=true, the packet should be dropped, so outputPacket should be nil
		assert.Nil(t, outputPacket2, "Output packet should be nil (dropped)")
	})

	t.Run("Test_Unknown_Prefix_and_Mapping_Handling_PrefixFalse_MappingFalse", func(t *testing.T) {
		// Configure NAT64 with drop_unknown_prefix=false and drop_unknown_mapping=false
		commands := []string{
			"/mnt/target/release/yanet-cli-nat64 drop --cfg nat64_0 --instances 0",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			if output != "" {
				t.Logf("Output: %s", output)
			}
		}

		// Test IPv6 packet with unknown prefix - should be passed through
		ipv6Packet := createNAT64Packet(
			net.ParseIP("2001:db9::3"),        // unknown prefix 2001:db9::/96 (different from configured 2001:db8::/96)
			net.ParseIP("2001:db9::c000:222"), // embedded IPv6 -> 192.0.2.34
			createTCPLayer(),
			[]byte("unknown prefix"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, ipv6Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present")
		// With drop_unknown_prefix=false, the packet should be passed through unchanged
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

		// Send packet and wait for response
		inputPacket2, outputPacket2, err := fw.SendPacketAndParse(0, 0, ipv4Packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be processed")

		require.NotNil(t, inputPacket2, "Input packet should be parsed")
		require.NotNil(t, outputPacket2, "Output packet should be present")
		// With drop_unknown_mapping=false, the packet should be passed through unchanged
		require.False(t, inputPacket2.IsIPv6, "Input packet should be IPv4")
		require.False(t, outputPacket2.IsIPv6, "Output packet should remain IPv4")
		assert.Equal(t, "192.0.2.100", outputPacket2.SrcIP.String(), "Source should remain unchanged")
		assert.Equal(t, "192.0.2.101", outputPacket2.DstIP.String(), "Destination should remain unchanged")
	})

	t.Run("Test_Default_Configuration_Values", func(t *testing.T) {
		// Test default configuration values for NAT64 module
		// This test checks that the NAT64 module is initialized with correct default values

		// Show current configuration
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-nat64 show --cfg nat64_0 --instances 0")
		require.NoError(t, err, "Failed to execute command: /mnt/target/release/yanet-cli-nat64 show --cfg nat64_0 --instances 0")

		// For now, just log the output to see what it returns
		t.Logf("Command output: %s", output)

		assert.Contains(t, output, "IPv4: 1450", "Default IPv4 MTU should be 1450")
		assert.Contains(t, output, "IPv6: 1280", "Default IPv6 MTU should be 1280")

		assert.Contains(t, output, "drop_unknown_prefix: false", "drop_unknown_prefix should be false by default")
		assert.Contains(t, output, "drop_unknown_mapping: false", "drop_unknown_mapping should be false by default")
	})
	time.Sleep(time.Second)
}

// Helper function to create NAT64 IPv4 TCP test packets
func createNAT64IPv4TCPPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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

// Helper function to create NAT64 IPv4 UDP test packets
func createNAT64IPv4UDPPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	udp := layers.UDP{
		SrcPort: 12345,
		DstPort: 53, // DNS port
	}
	err := udp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4, &udp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Helper function to create NAT64 IPv4 ICMP test packets
func createNAT64IPv4ICMPPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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
		Id:       0x1234,
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
