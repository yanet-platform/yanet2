package functional

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// createRouteTestPacket creates a TCP packet for route testing
func createRouteTestPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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

// TestRoute tests route module functionality including static route insertion and deletion
func TestRoute(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Insert_Static_Route", func(t *testing.T) {
		// Add neighbour for the nexthop
		_, err := fw.CLI.ExecuteCommand("ip nei add 192.0.2.1 lladdr " + framework.SrcMAC + " dev kni0")
		require.NoError(t, err, "Failed to add neighbour")

		// Wait for neighbour to appear in yanet
		err = fw.WaitOutputPresent("/mnt/target/release/yanet-cli-neighbour show", func(output string) bool {
			return strings.Contains(output, "192.0.2.1")
		}, 10*time.Second)
		require.NoError(t, err, "Neighbour 192.0.2.1 did not appear in yanet")

		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route insert --cfg route-tfn0 10.0.0.0/24 --via 192.0.2.1")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
		t.Logf("Successfully inserted route 10.0.0.0/24 via 192.0.2.1")
	})

	t.Run("Configure_Route_Module", func(t *testing.T) {
		// Configure route module
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=route:route-tfn0",
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure route module")
	})

	t.Run("List_Configs", func(t *testing.T) {
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route list")
		require.NoError(t, err, "Failed to list configs")
		t.Logf("Available configs: %s", output)
	})

	t.Run("Show_Routes_After_Insert", func(t *testing.T) {
		// First, show all routes without filter
		outputAll, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route show --cfg route-tfn0")
		require.NoError(t, err, "Failed to show all routes")
		t.Logf("Show all routes output:\n%s", outputAll)

		// Then, show only IPv4 routes with filter
		outputIPv4, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route show --cfg route-tfn0 --ipv4")
		require.NoError(t, err, "Failed to show IPv4 routes")

		// Verify our route is present in filtered output
		require.Contains(t, outputIPv4, "10.0.0.0/24", "Inserted route prefix should be present")
		require.Contains(t, outputIPv4, "192.0.2.1", "Inserted route nexthop should be present")
		require.Contains(t, outputIPv4, "static", "Route should be marked as 'static'")
		t.Logf("Show IPv4 routes (filtered) output:\n%s", outputIPv4)
	})

	t.Run("Lookup_Route", func(t *testing.T) {
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route lookup --cfg route-tfn0 10.0.0.10")
		require.NoError(t, err, "Failed to lookup route")

		// Verify lookup result contains our prefix
		require.Contains(t, output, "10.0.0.0/24", "Lookup should match our test prefix")
		t.Logf("Lookup output:\n%s", output)
	})

	t.Run("Test_Packet_Routing_With_Route", func(t *testing.T) {
		fw := globalFramework.WithTestName(t.Name())

		// Create packet destined to our routed network
		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("10.0.0.10"),   // dst IP in our routed network
			[]byte("route test"),
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		if outputPacket != nil {
			t.Logf("Packet routed: src=%s dst=%s", outputPacket.SrcIP, outputPacket.DstIP)
		}
	})

	t.Run("Delete_Static_Route", func(t *testing.T) {
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route delete --cfg route-tfn0 10.0.0.0/24 --via 192.0.2.1")
		require.NoError(t, err, "Failed to delete route")
		t.Logf("Delete route output: %s", output)
		t.Logf("Successfully deleted route 10.0.0.0/24 via 192.0.2.1")

		// Clean up neighbour
		_, err = fw.CLI.ExecuteCommand("ip nei del 192.0.2.1 dev kni0")
		require.NoError(t, err, "Failed to delete neighbour")
	})

	t.Run("Show_Routes_After_Delete", func(t *testing.T) {
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route show --cfg route-tfn0 --ipv4")
		require.NoError(t, err, "Failed to show routes")

		// Verify our static route is deleted
		// The output should either not contain the prefix, or if it does, it shouldn't be marked as static
		if strings.Contains(output, "10.0.0.0/24") {
			// If prefix is still there, it should not be from static source
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				if strings.Contains(line, "10.0.0.0/24") && strings.Contains(line, "192.0.2.1") {
					assert.NotContains(t, line, "static", "Deleted static route should not be present")
				}
			}
		}
		t.Logf("Verified route deletion. Show routes output:\n%s", output)
	})

	t.Run("Test_Packet_Without_Route", func(t *testing.T) {
		fw := globalFramework.WithTestName(t.Name())

		// Create packet destined to a network without any route
		// Without default route, this packet should be dropped
		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("172.16.0.10"), // dst IP - no route for this network
			[]byte("no route test"),
		)

		// Try to receive packet on interface 0
		inputPacket, outputPacket0, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr0 net.Error
		require.ErrorAs(t, err, &netErr0, "Error should be a net.Error")
		require.True(t, netErr0.Timeout(), "Error should be a timeout")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket0, "Packet should be dropped (no route) on interface 0")

		// Also check interface 1 to be sure
		outputPacket1, err := fw.SendPacketAndCapture(0, 1, packet, 100*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr1 net.Error
		require.ErrorAs(t, err, &netErr1, "Error should be a net.Error")
		require.True(t, netErr1.Timeout(), "Error should be a timeout")
		require.Nil(t, outputPacket1, "Packet should be dropped (no route) on interface 1")

		t.Logf("Packet correctly dropped (no matching route)")
	})

	t.Run("Test_Packet_With_Default_Route", func(t *testing.T) {
		// Neighbour for gateway already added in CommonConfigCommands (framework.VMIPv4Gateway)

		// Insert default route using the gateway from framework into route-tfn0 config
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route insert --cfg route-tfn0 0.0.0.0/0 --via " + framework.VMIPv4Gateway)
		require.NoError(t, err, "Failed to insert default route")
		t.Logf("Inserted default route: %s", output)

		fw := globalFramework.WithTestName(t.Name())

		// Create packet destined to a network without specific route
		// This should now match the default route
		packet := createRouteTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("172.16.0.10"), // dst IP - will use default route
			[]byte("default route test"),
		)

		// Send packet and expect it to be routed via default route on interface 0
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")

		// With default route configured, packet should be routed
		require.NotNil(t, outputPacket, "Packet should be routed via default route")
		require.Equal(t, "172.16.0.10", outputPacket.DstIP.String(), "Destination IP should be preserved")
		t.Logf("Packet correctly routed via default route: src=%s dst=%s", outputPacket.SrcIP, outputPacket.DstIP)

		// Clean up: delete default route
		output, err = fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route delete --cfg route-tfn0 0.0.0.0/0 --via " + framework.VMIPv4Gateway)
		require.NoError(t, err, "Failed to delete default route")
		t.Logf("Deleted default route: %s", output)
	})

	t.Run("Insert_Multiple_Routes", func(t *testing.T) {
		routes := []struct {
			prefix  string
			nexthop string
		}{
			{"10.1.0.0/24", "192.0.2.1"},
			{"10.2.0.0/24", "192.0.2.2"},
			{"10.3.0.0/24", "192.0.2.3"},
		}

		// Add neighbours for all nexthops
		for _, route := range routes {
			_, err := fw.CLI.ExecuteCommand("ip nei add " + route.nexthop + " lladdr " + framework.SrcMAC + " dev kni0")
			require.NoError(t, err, "Failed to add neighbour for %s", route.nexthop)
		}

		// Wait for all neighbours to appear
		for _, route := range routes {
			err := fw.WaitOutputPresent("/mnt/target/release/yanet-cli-neighbour show", func(output string) bool {
				return strings.Contains(output, route.nexthop)
			}, 10*time.Second)
			require.NoError(t, err, "Neighbour %s did not appear in yanet", route.nexthop)
		}

		// Insert routes
		for _, route := range routes {
			_, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route insert --cfg route-tfn0 " + route.prefix + " --via " + route.nexthop)
			require.NoError(t, err, "Failed to insert route %s", route.prefix)
		}

		// Flush routes
		_, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route flush --cfg route-tfn0")
		require.NoError(t, err, "Failed to flush routes")

		// Verify all routes are present
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route show --cfg route-tfn0 --ipv4")
		require.NoError(t, err, "Failed to show routes")

		for _, route := range routes {
			require.Contains(t, output, route.prefix, "Route %s should be present", route.prefix)
			require.Contains(t, output, route.nexthop, "Nexthop %s should be present", route.nexthop)
		}

		t.Logf("Successfully inserted and verified %d routes", len(routes))
	})

	t.Run("Delete_Multiple_Routes", func(t *testing.T) {
		routes := []struct {
			prefix  string
			nexthop string
		}{
			{"10.1.0.0/24", "192.0.2.1"},
			{"10.2.0.0/24", "192.0.2.2"},
			{"10.3.0.0/24", "192.0.2.3"},
		}

		// Delete routes
		for _, route := range routes {
			_, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route delete --cfg route-tfn0 " + route.prefix + " --via " + route.nexthop)
			require.NoError(t, err, "Failed to delete route %s", route.prefix)
		}

		// Clean up neighbours
		for _, route := range routes {
			_, err := fw.CLI.ExecuteCommand("ip nei del " + route.nexthop + " dev kni0")
			require.NoError(t, err, "Failed to delete neighbour for %s", route.nexthop)
		}

		// Flush routes
		_, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route flush --cfg route-tfn0")
		require.NoError(t, err, "Failed to flush routes")

		// Verify all static routes are deleted
		output, err := fw.CLI.ExecuteCommand("/mnt/target/release/yanet-cli-route show --cfg route-tfn0 --ipv4")
		require.NoError(t, err, "Failed to show routes")

		for _, route := range routes {
			// Check that if the prefix exists, it's not from static source
			if strings.Contains(output, route.prefix) {
				lines := strings.Split(output, "\n")
				for _, line := range lines {
					if strings.Contains(line, route.prefix) && strings.Contains(line, route.nexthop) {
						assert.NotContains(t, line, "static", "Route %s via %s should not be static", route.prefix, route.nexthop)
					}
				}
			}
		}

		t.Logf("Successfully deleted and verified %d routes", len(routes))
	})
}
