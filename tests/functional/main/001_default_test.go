package functional

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"github.com/yanet-platform/yanet2/tests/migration/converter/lib"
)

// TestTest_001_default - automatically generated test from yanet1
// Original test: 001_default
// Test type: route
func TestTest_001_default(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		require.NotNil(t, fw, "Global framework should be initialized")
		// Silence potentially unused imports PCAP vs AST parser
		_ = cmp.Diff
		_ = lib.CmpStdOpts
		_ = lib.NewPacket
		_ = net.ParseIP
		_ = strings.Join

		fw.Run("Step_001_Configure_Routes", func(fw *framework.TestFramework, t *testing.T) {
			// IPv4 routes configuration
			// Original autotest.yaml step:
			// ipv4Update:
			//   - "0.0.0.0/0 -> 200.0.0.1"
			fibYAML := `
entries:
  - prefix: "0.0.0.0/0"
    nexthops:
      - dst_mac: "52:54:00:6b:ff:a1"
        src_mac: "52:54:00:6b:ff:a5"
        device: "01:00.0"
  - prefix: "::/0"
    nexthops:
      - dst_mac: "52:54:00:6b:ff:a1"
        src_mac: "52:54:00:6b:ff:a5"
        device: "01:00.0"
`
			err := fw.CreateConfigFile("route0-step001.yaml", fibYAML)
			require.NoError(t, err, "Failed to create FIB config for IPv4 routes")
			_, err = fw.ExecuteCommand("/mnt/target/release/yanet-cli-route fib update --name=route0 --rules /mnt/config/route0-step001.yaml")
			require.NoError(t, err, "Failed to update FIB for IPv4 routes")
		})

		// Wait 3 seconds for configuration changes to take effect (pipeline updates are asynchronous)
		time.Sleep(3 * time.Second)

		fw.Run("Step_001_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: send.pcap -> expect.pcap
			sendPackets := create001_defaultSendPacket1(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create001_defaultExpectPacket1(t)
			require.NotNil(t, expectedPackets)

			// Get socket client
			client, err := fw.GetSocketClient(0)
			require.NoError(t, err, "Failed to get socket client")
			defer client.Close()
			require.NoError(t, client.Connect(), "Failed to connect to socket")

			var receivedPackets []gopacket.Packet
			for idx, pkt := range sendPackets {
				packetBytes := pkt.Data()

				// Send packet
				require.NoError(t, client.SendPacket(packetBytes, ""), "Failed to send packet %d", idx)

				// Receive packet (ignore errors - packet may be dropped)
				responseData, _ := client.ReceivePacket(100*time.Millisecond, "")
				if responseData != nil {
					receivedPkt := gopacket.NewPacket(responseData, layers.LayerTypeEthernet, gopacket.Default)
					receivedPackets = append(receivedPackets, receivedPkt)
				}

				// Small delay to prevent socket buffer overflow when sending many packets rapidly
				// This gives the dataplane time to process packets before the socket buffer fills up
				if idx < len(sendPackets)-1 {
					time.Sleep(1 * time.Millisecond)
				}
			}

			// Validate all received packets against expected packets
			t.Logf("Received %d packets, expected %d packets", len(receivedPackets), len(expectedPackets))

			require.Equalf(t, len(expectedPackets), len(receivedPackets),
				"Packet count mismatch: expected %d, received %d", len(expectedPackets), len(receivedPackets))

			for idx, expectedPkt := range expectedPackets {
				actualPkt := receivedPackets[idx]

				diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(), lib.CmpStdOpts...)
				require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)
			}
		})
	})
}

// reading from file 001_default/send.pcap, link-type EN10MB (Ethernet)
// 2019-03-26 11:57:29.181021 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 46: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto ICMP (1), length 28)
//
//	0.0.0.0 > 1.1.1.1: ICMP echo request, id 0, seq 0, length 8
//
// create001_defaultSendPacket1 generates packets
func create001_defaultSendPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a5"),
				lib.EtherSrc("52:54:00:6b:ff:a1"),
			),
			lib.IPv4(
				lib.IPSrc("0.0.0.0"),
				lib.IPDst("1.1.1.1"),
				lib.IPTTL(64),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(1),
				lib.IPv4Length(28),
				lib.IPv4ChecksumRaw(30943),
			),
			lib.ICMP(
				lib.ICMPTypeCode(8, 0),
				lib.ICMPChecksum(63487),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}

// reading from file 001_default/expect.pcap, link-type EN10MB (Ethernet)
// 2019-03-26 12:55:08.333949 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 46: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto ICMP (1), length 28)
//
//	0.0.0.0 > 1.1.1.1: ICMP echo request, id 0, seq 0, length 8
//
// create001_defaultExpectPacket1 generates packets
func create001_defaultExpectPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a1"),
				lib.EtherSrc("52:54:00:6b:ff:a5"),
			),
			lib.IPv4(
				lib.IPSrc("0.0.0.0"),
				lib.IPDst("1.1.1.1"),
				lib.IPTTL(63),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(1),
				lib.IPv4Length(28),
				lib.IPv4ChecksumRaw(31199),
			),
			lib.ICMP(
				lib.ICMPTypeCode(8, 0),
				lib.ICMPChecksum(63487),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}
