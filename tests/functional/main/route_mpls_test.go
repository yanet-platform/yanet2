package functional

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// routeMPLSCfgName is the route module config used by the route-mpls
// tests. The route-mpls module shares the cfg name with the route
// module that backs its egress lookups.
const routeMPLSCfgName = "route-mpls"

// TestRouteMPLS tests route module functionality including static route insertion and deletion
func TestRouteMPLS(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testRouteMPLS(t, fw)
	})
}

func testRouteMPLS(t *testing.T, fw *framework.TestFramework) {

	fw.Run("Insert_Static_Routes", func(fw *framework.TestFramework, t *testing.T) {
		// Push the IPv4 and IPv6 prefixes the MPLS tunnel decisions
		// depend on as a single atomic FIB update.
		applyFIB(t, fw, routeMPLSCfgName, "setup", "10.0.0.0/8", "ccee::0/16")
		t.Logf("Successfully inserted routes 10.0.0.0/8 and ccee::0/16")
	})

	fw.Run("Insert_Static_RouteMPLS-4-4", func(fw *framework.TestFramework, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand(framework.CLIRouteMPLS + " update --name route-mpls -p 5.0.0.0/9 --dst 10.12.1.1 --src 4.2.4.2 --label 45 --weight 5 --counter 4-4")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-4-6", func(fw *framework.TestFramework, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand(framework.CLIRouteMPLS + " update --name route-mpls -p 6.0.0.0/10 --dst ccee::11 --src 2424::1212 --label 45 --weight 5 --counter 4-6")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-6-4", func(fw *framework.TestFramework, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand(framework.CLIRouteMPLS + " update --name route-mpls -p 0066::0/17 --dst 10.12.1.1 --src 4.2.4.2 --label 45 --weight 5 --counter 6-4")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-6-6", func(fw *framework.TestFramework, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand(framework.CLIRouteMPLS + " update --name route-mpls -p 0088::0/19 --dst ccee::11 --src 2424::1212 --label 45 --weight 5 --counter 6-6")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Configure_RouteMPLS_Module", func(fw *framework.TestFramework, t *testing.T) {
		// Configure route module
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=route-mpls:route-mpls,route:route-mpls",
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure route module")
	})

	fw.Run("List_Configs", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(framework.CLIRouteMPLS + " list")
		require.NoError(t, err, "Failed to list configs")
		t.Logf("Available configs: %s", output)
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-4-4", func(fw *framework.TestFramework, t *testing.T) {

		// Create packet destined to our routed network
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("5.0.0.10"),    // dst IP in our mpls network
			[]byte("route test"),
			nil,
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("10.12.1.1").To4(), "Invalid tunnel destiation")
		require.True(t, outputPacket.SrcPort >= uint16(0xc000), "Invalid source port")
		require.Equal(t, outputPacket.DstPort, uint16(6635), "Invalid destination port")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-4-6", func(fw *framework.TestFramework, t *testing.T) {

		// Create packet destined to our routed network
		packet := framework.CreateTCPIPv4Packet(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("6.0.0.10"),    // dst IP in our mpls network
			[]byte("route test"),
			nil,
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("ccee::11"), "Invalid tunnel destiation")
		require.True(t, outputPacket.SrcPort >= uint16(0xc000), "Invalid source port")
		require.Equal(t, outputPacket.DstPort, uint16(6635), "Invalid destination port")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-6-4", func(fw *framework.TestFramework, t *testing.T) {

		// Create packet destined to our routed network
		packet := framework.CreateTCPIPv6Packet(
			net.ParseIP("aa66:2212::1"),      // src IP
			net.ParseIP("0066:1223:34:3::1"), // dst IP in our mpls network
			[]byte("route test"),
			nil,
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("10.12.1.1").To4(), "Invalid tunnel destiation")
		require.True(t, outputPacket.SrcPort >= uint16(0xc000), "Invalid source port")
		require.Equal(t, outputPacket.DstPort, uint16(6635), "Invalid destination port")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-6-6", func(fw *framework.TestFramework, t *testing.T) {

		// Create packet destined to our routed network
		packet := framework.CreateTCPIPv6Packet(
			net.ParseIP("aa66:2212::1"),      // src IP
			net.ParseIP("0088:1223:34:3::1"), // dst IP in our mpls network
			[]byte("route test"),
			nil,
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("ccee::11"), "Invalid tunnel destiation")
		require.True(t, outputPacket.SrcPort >= uint16(0xc000), "Invalid source port")
		require.Equal(t, outputPacket.DstPort, uint16(6635), "Invalid destination port")
	})

}
