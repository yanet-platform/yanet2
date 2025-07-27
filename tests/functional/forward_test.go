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
	tcp.SetNetworkLayerForChecksum(&ip4)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip4, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createL2ForwardPacket creates a simple Ethernet frame for L2 forwarding testing
func createL2ForwardPacket(payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// TestForward_BasicFunctionality tests basic forward module functionality
func TestForward_BasicFunctionality(t *testing.T) {
	// Use global framework instance like in TestYANETStartup
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_Forward_Module", func(t *testing.T) {
		// Configure forward module (L2 and L3 forwarding)
		commands := []string{
			"ip link set kni0 up",
			"ip nei add fe80::1 lladdr 52:54:00:6b:ff:a1 dev kni0",
			"ip nei add 203.0.113.1 lladdr 52:54:00:6b:ff:a1 dev kni0",
			"sleep 3",
			// Enable L2 forwarding between devices
			"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 0 --dst 1",
			"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 1 --dst 0",
			// Add L3 forwarding rules
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net 192.0.2.0/24",
			"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net 0.0.0.0/0",
			"/mnt/target/release/yanet-cli-forward show --cfg=forward0 --instances 0",

			// Route
			"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via fe80::1 ::/0",
			"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via 203.0.113.1 0.0.0.0/0",

			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=bootstrap --modules forward:forward0 --instance=0",
			"/mnt/target/release/yanet-cli-pipeline update --name=forward --modules forward:forward0 --modules route:route0 --instance=0",

			// Assign pipelines to devices
			"/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=01:00.0 --pipelines forward:1",
			"/mnt/target/release/yanet-cli-pipeline assign --instance=0 --device=virtio_user_kni0 --pipelines bootstrap:1",

			// Inspect configuration
			"/mnt/target/release/yanet-cli-inspect",
			"/mnt/target/release/yanet-cli-route show --cfg route0 --instances 0",

			// Copy logs for debugging
			"cp /var/log/yanet-controlplane.log /mnt/build/ 2>/dev/null || echo 'No controlplane log found'",
			"cp /var/log/yanet-dataplane.log /mnt/build/ 2>/dev/null || echo 'No dataplane log found'",
		}

		for _, cmd := range commands {
			output, err := fw.CLI.ExecuteCommand(cmd)
			require.NoError(t, err, "Failed to execute command: %s", cmd)
			t.Logf("Output: %s", output)
		}
	})

	t.Run("Test_L3_Forwarding", func(t *testing.T) {
		// Test L3 forwarding within configured network
		packet := createForwardPacket(
			net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
			net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
			[]byte("forward test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify packet was forwarded with preserved addresses
		assert.Equal(t, "192.0.2.1", outputPacket.SrcIP.String(), "Source IP should be preserved")
		assert.Equal(t, "192.0.2.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
	})

	t.Run("Test_L2_Forwarding", func(t *testing.T) {
		// Test L2 forwarding (MAC-based)
		packet := createL2ForwardPacket(
			[]byte("l2 forward test"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send L2 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// For L2 forwarding, we mainly verify the packet was processed
		// MAC addresses might be modified by the forwarding process
	})

	t.Run("Test_Non_Matching_Network", func(t *testing.T) {
		// Test packet to network not in forwarding table
		packet := createForwardPacket(
			net.ParseIP("10.0.0.1"), // src IP (not in forwarding rules)
			net.ParseIP("10.0.0.2"), // dst IP (not in forwarding rules)
			[]byte("no route"),
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send non-matching packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")

		if outputPacket != nil {
			// Packet might be forwarded via default route
			assert.Equal(t, "10.0.0.1", outputPacket.SrcIP.String(), "Source IP should be preserved")
			assert.Equal(t, "10.0.0.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
		}
	})
}
