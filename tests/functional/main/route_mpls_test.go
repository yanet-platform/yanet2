package functional

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// createRouteMPLSTestPacket creates a TCP packet for route testing
func createRouteMPLSTestPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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

func createRoute6MPLSTestPacket(srcIP, dstIP net.IP, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
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

// TestRouteMPLS tests route module functionality including static route insertion and deletion
func TestRouteMPLS(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Insert_Static_Route4", func(fw *framework.F, t *testing.T) {
		// Add neighbour for the nexthop
		_, err := fw.ExecuteCommand("ip nei add 192.0.2.1 lladdr " + framework.SrcMAC + " dev kni0")
		require.NoError(t, err, "Failed to add neighbour")

		// Wait for neighbour to appear in yanet
		err = fw.WaitOutputPresent("/mnt/target/release/yanet-cli-neighbour show", func(output string) bool {
			return strings.Contains(output, "192.0.2.1")
		}, 10*time.Second)
		require.NoError(t, err, "Neighbour 192.0.2.1 did not appear in yanet")

		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route insert --cfg route-mpls 10.0.0.0/8 --via 192.0.2.1")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
		t.Logf("Successfully inserted route 10.0.0.0/8 via 192.0.2.1")
	})

	fw.Run("Insert_Static_Route6", func(fw *framework.F, t *testing.T) {
		// Add neighbour for the nexthop
		_, err := fw.ExecuteCommand("ip -6 nei add aabb::1 lladdr " + framework.SrcMAC + " dev kni0")
		require.NoError(t, err, "Failed to add neighbour")

		// Wait for neighbour to appear in yanet
		err = fw.WaitOutputPresent("/mnt/target/release/yanet-cli-neighbour show", func(output string) bool {
			return strings.Contains(output, "aabb::1")
		}, 10*time.Second)
		require.NoError(t, err, "Neighbour aabb::1 did not appear in yanet")

		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route insert --cfg route-mpls ccee::0/16 --via aabb::1")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
		t.Logf("Successfully inserted route ccee::0/16 via aabb::1")
	})

	fw.Run("Insert_Static_RouteMPLS-4-4", func(fw *framework.F, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route-mpls update --cfg route-mpls -p 5.0.0.0/9 --dst 10.12.1.1 --src 4.2.4.2 --label 45 --as_path 14 --local_pref 10 --med 10 --weight 5 --counter 4-4")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-4-6", func(fw *framework.F, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route-mpls update --cfg route-mpls -p 6.0.0.0/10 --dst ccee::11 --src 2424::1212 --label 45 --as_path 14 --local_pref 10 --med 10 --weight 5 --counter 4-6")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-6-4", func(fw *framework.F, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route-mpls update --cfg route-mpls -p 0066::0/17 --dst 10.12.1.1 --src 4.2.4.2 --label 45 --as_path 14 --local_pref 10 --med 10 --weight 5 --counter 6-4")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Insert_Static_RouteMPLS-6-6", func(fw *framework.F, t *testing.T) {
		// Insert route with the nexthop (do_flush is automatic in insert command)
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route-mpls update --cfg route-mpls -p 0088::0/19 --dst ccee::11 --src 2424::1212 --label 45 --as_path 14 --local_pref 10 --med 10 --weight 5 --counter 6-6")
		require.NoError(t, err, "Failed to insert route")
		t.Logf("Insert route output: %s", output)
	})

	fw.Run("Configure_RouteMPLS_Module", func(fw *framework.F, t *testing.T) {
		// Configure route module
		commands := []string{
			framework.CLIFunction + " update --name=test --chains ch0:4=route-mpls:route-mpls,route:route-mpls",
			framework.CLIPipeline + " update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure route module")
	})

	fw.Run("List_Configs", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route-mpls list")
		require.NoError(t, err, "Failed to list configs")
		t.Logf("Available configs: %s", output)
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-4-4", func(fw *framework.F, t *testing.T) {

		// Create packet destined to our routed network
		packet := createRouteMPLSTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("5.0.0.10"),    // dst IP in our mpls network
			[]byte("route test"),
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("10.12.1.1").To4(), "Invalid uneel destiation")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-4-6", func(fw *framework.F, t *testing.T) {

		// Create packet destined to our routed network
		packet := createRouteMPLSTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("6.0.0.10"),    // dst IP in our mpls network
			[]byte("route test"),
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("ccee::11"), "Invalid uneel destiation")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-6-4", func(fw *framework.F, t *testing.T) {

		// Create packet destined to our routed network
		packet := createRoute6MPLSTestPacket(
			net.ParseIP("aa66:2212::1"),      // src IP
			net.ParseIP("0066:1223:34:3::1"), // dst IP in our mpls network
			[]byte("route test"),
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("10.12.1.1").To4(), "Invalid uneel destiation")
	})

	fw.Run("Test_Packet_Routing_With_RouteMPLS-6-6", func(fw *framework.F, t *testing.T) {

		// Create packet destined to our routed network
		packet := createRoute6MPLSTestPacket(
			net.ParseIP("aa66:2212::1"),      // src IP
			net.ParseIP("0088:1223:34:3::1"), // dst IP in our mpls network
			[]byte("route test"),
		)

		// Send packet and check if it's routed
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.Equal(t, outputPacket.DstIP, net.ParseIP("ccee::11"), "Invalid uneel destiation")
	})

	fw.Run("Delete_Static_Route", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand("/mnt/target/release/yanet-cli-route remove --cfg route-mpls 10.0.0.0/24 --via 192.0.2.1")
		require.NoError(t, err, "Failed to delete route")
		t.Logf("Delete route output: %s", output)
		t.Logf("Successfully deleted route 10.0.0.0/8 via 192.0.2.1")

		// Clean up neighbour
		_, err = fw.ExecuteCommand("ip nei del 192.0.2.1 dev kni0")
		require.NoError(t, err, "Failed to delete neighbour")
	})
}
