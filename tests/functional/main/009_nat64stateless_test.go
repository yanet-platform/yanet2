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

// TestTest_009_nat64stateless - automatically generated test from yanet1
// Original test: 009_nat64stateless
// Test type: nat64
func TestTest_009_nat64stateless(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		require.NotNil(t, fw, "Global framework should be initialized")
		// Silence potentially unused imports PCAP vs AST parser
		_ = cmp.Diff
		_ = lib.CmpStdOpts
		_ = lib.NewPacket
		_ = net.ParseIP
		_ = strings.Join

		fw.Run("Step_000_Configure_NAT64_Environment", func(fw *framework.TestFramework, t *testing.T) {
			// Configure NAT64 module
			commands := []string{
				"/mnt/target/release/yanet-cli-nat64 prefix add --name nat64stateless0 --prefix 5555:5555:5555:5555:5555:5555::/96",
				"/mnt/target/release/yanet-cli-nat64 mapping add --name nat64stateless0 --ipv4 153.153.153.153 --ipv6 aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa --prefix-index 0",

				"/mnt/target/release/yanet-cli-function update --name=test --chains chain2:1=forward:forward0,nat64:nat64stateless0,route:route0",
				"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test",
			}
			_, err := fw.ExecuteCommands(commands...)
			require.NoError(t, err, "Failed to configure NAT64 module")

		})

		fw.Run("Step_001_Configure_Routes", func(fw *framework.TestFramework, t *testing.T) {
			// IPv4 routes configuration
			// Original autotest.yaml step:
			// ipv4Update:
			//   - "102.102.102.102/31 -> 200.0.0.1"
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
  - prefix: "102.102.102.102/31"
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
		fw.Run("Step_002_Configure_Routes", func(fw *framework.TestFramework, t *testing.T) {
			// IPv6 routes configuration
			// Original autotest.yaml step:
			// ipv6Update:
			//   - "aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa/128 -> aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:1"
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
  - prefix: "102.102.102.102/31"
    nexthops:
      - dst_mac: "52:54:00:6b:ff:a1"
        src_mac: "52:54:00:6b:ff:a5"
        device: "01:00.0"
  - prefix: "aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa/128"
    nexthops:
      - dst_mac: "52:54:00:6b:ff:a1"
        src_mac: "52:54:00:6b:ff:a5"
        device: "01:00.0"
`
			err := fw.CreateConfigFile("route0-step002.yaml", fibYAML)
			require.NoError(t, err, "Failed to create FIB config for IPv6 routes")
			_, err = fw.ExecuteCommand("/mnt/target/release/yanet-cli-route fib update --name=route0 --rules /mnt/config/route0-step002.yaml")
			require.NoError(t, err, "Failed to update FIB for IPv6 routes")
		})

		// Wait 3 seconds for configuration changes to take effect (pipeline updates are asynchronous)
		time.Sleep(3 * time.Second)

		fw.Run("Step_001_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: 001-send.pcap -> 001-expect.pcap
			sendPackets := create009_nat64statelessSendPacket1(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create009_nat64statelessExpectPacket1(t)
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
		fw.Run("Step_002_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: 002-send.pcap -> 002-expect.pcap
			sendPackets := create009_nat64statelessSendPacket2(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create009_nat64statelessExpectPacket2(t)
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
		fw.Run("Step_003_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: 007-send.pcap -> 007-expect.pcap
			sendPackets := create009_nat64statelessSendPacket3(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create009_nat64statelessExpectPacket3(t)
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
		fw.Run("Step_004_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: 008-send.pcap -> 008-expect.pcap
			sendPackets := create009_nat64statelessSendPacket4(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create009_nat64statelessExpectPacket4(t)
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

// reading from file 009_nat64stateless/001-send.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6666.80: Flags [S], cksum 0x6571 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6666.443: Flags [S], cksum 0x6406 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6667.80: Flags [S], cksum 0x6570 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6667.443: Flags [S], cksum 0x6405 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6666.80: Flags [S], cksum 0x6571 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6666.443: Flags [S], cksum 0x6406 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6667.80: Flags [S], cksum 0x6570 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header TCP (6) payload length: 20) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048 > 5555:5555:5555:5555:5555:5555:6666:6667.443: Flags [S], cksum 0x6405 (correct), seq 0, win 8192, length 0
// create009_nat64statelessSendPacket1Params holds varying parameters for packet generation
type create009_nat64statelessSendPacket1Params struct {
	Ipv6Dst   string
	TcpDport  uint16
	TcpChksum uint16
}

// create009_nat64statelessSendPacket1Helper generates a single packet with varying parameters
func create009_nat64statelessSendPacket1Helper(t *testing.T, params create009_nat64statelessSendPacket1Params) gopacket.Packet {
	pkt, err := lib.NewPacket(nil,
		lib.Ether(
			lib.EtherDst("52:54:00:6b:ff:a5"),
			lib.EtherSrc("52:54:00:6b:ff:a1"),
		),
		lib.IPv6(
			lib.IPv6Src("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
			lib.IPv6Dst(params.Ipv6Dst),
			lib.IPv6HopLimit(64),
		),
		lib.TCP(
			lib.TCPSport(2048),
			lib.TCPDport(params.TcpDport),
			lib.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create009_nat64statelessSendPacket1 generates packets
func create009_nat64statelessSendPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-7 (using helper)
	paramsList := []create009_nat64statelessSendPacket1Params{
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6666", TcpDport: 80, TcpChksum: 25969},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6666", TcpDport: 443, TcpChksum: 25606},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6667", TcpDport: 80, TcpChksum: 25968},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6667", TcpDport: 443, TcpChksum: 25605},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6666", TcpDport: 80, TcpChksum: 25969},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6666", TcpDport: 443, TcpChksum: 25606},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6667", TcpDport: 80, TcpChksum: 25968},
		{Ipv6Dst: "5555:5555:5555:5555:5555:5555:6666:6667", TcpDport: 443, TcpChksum: 25605},
	}

	for _, params := range paramsList {
		packets = append(packets, create009_nat64statelessSendPacket1Helper(t, params))
	}

	return packets
}

// reading from file 009_nat64stateless/001-expect.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.102.80: Flags [S], cksum 0x8793 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.102.443: Flags [S], cksum 0x8628 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.103.80: Flags [S], cksum 0x8792 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.103.443: Flags [S], cksum 0x8627 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.102.80: Flags [S], cksum 0x8793 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.102.443: Flags [S], cksum 0x8628 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.103.80: Flags [S], cksum 0x8792 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto TCP (6), length 40)
//
//	153.153.153.153.2048 > 102.102.102.103.443: Flags [S], cksum 0x8627 (correct), seq 0, win 8192, length 0
//
// create009_nat64statelessExpectPacket1Params holds varying parameters for packet generation
type create009_nat64statelessExpectPacket1Params struct {
	Ipv4Chksum uint16
	Ipv4Dst    string
	TcpChksum  uint16
	TcpDport   uint16
}

// create009_nat64statelessExpectPacket1Helper generates a single packet with varying parameters
func create009_nat64statelessExpectPacket1Helper(t *testing.T, params create009_nat64statelessExpectPacket1Params) gopacket.Packet {
	pkt, err := lib.NewPacket(nil,
		lib.Ether(
			lib.EtherDst("52:54:00:6b:ff:a1"),
			lib.EtherSrc("52:54:00:6b:ff:a5"),
		),
		lib.IPv4(
			lib.IPSrc("153.153.153.153"),
			lib.IPDst(params.Ipv4Dst),
			lib.IPTTL(63),
			lib.IPId(0),
		),
		lib.TCP(
			lib.TCPSport(2048),
			lib.TCPDport(params.TcpDport),
			lib.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create009_nat64statelessExpectPacket1 generates packets
func create009_nat64statelessExpectPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-7 (using helper)
	paramsList := []create009_nat64statelessExpectPacket1Params{
		{Ipv4Chksum: 31697, Ipv4Dst: "102.102.102.102", TcpChksum: 34707, TcpDport: 80},
		{Ipv4Chksum: 31697, Ipv4Dst: "102.102.102.102", TcpChksum: 34344, TcpDport: 443},
		{Ipv4Chksum: 31696, Ipv4Dst: "102.102.102.103", TcpChksum: 34706, TcpDport: 80},
		{Ipv4Chksum: 31696, Ipv4Dst: "102.102.102.103", TcpChksum: 34343, TcpDport: 443},
		{Ipv4Chksum: 31697, Ipv4Dst: "102.102.102.102", TcpChksum: 34707, TcpDport: 80},
		{Ipv4Chksum: 31697, Ipv4Dst: "102.102.102.102", TcpChksum: 34344, TcpDport: 443},
		{Ipv4Chksum: 31696, Ipv4Dst: "102.102.102.103", TcpChksum: 34706, TcpDport: 80},
		{Ipv4Chksum: 31696, Ipv4Dst: "102.102.102.103", TcpChksum: 34343, TcpDport: 443},
	}

	for _, params := range paramsList {
		packets = append(packets, create009_nat64statelessExpectPacket1Helper(t, params))
	}

	return packets
}

// reading from file 009_nat64stateless/002-send.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.102.80 > 153.153.153.153.2048: Flags [S], cksum 0x8793 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.102.443 > 153.153.153.153.2048: Flags [S], cksum 0x8628 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.103.80 > 153.153.153.153.2048: Flags [S], cksum 0x8792 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.103.443 > 153.153.153.153.2048: Flags [S], cksum 0x8627 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.102.80 > 153.153.153.153.2048: Flags [S], cksum 0x8793 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.102.443 > 153.153.153.153.2048: Flags [S], cksum 0x8628 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.103.80 > 153.153.153.153.2048: Flags [S], cksum 0x8792 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	102.102.102.103.443 > 153.153.153.153.2048: Flags [S], cksum 0x8627 (correct), seq 0, win 8192, length 0
//
// create009_nat64statelessSendPacket2Params holds varying parameters for packet generation
type create009_nat64statelessSendPacket2Params struct {
	Ipv4Src    string
	Ipv4Chksum uint16
	TcpSport   uint16
	TcpChksum  uint16
}

// create009_nat64statelessSendPacket2Helper generates a single packet with varying parameters
func create009_nat64statelessSendPacket2Helper(t *testing.T, params create009_nat64statelessSendPacket2Params) gopacket.Packet {
	pkt, err := lib.NewPacket(nil,
		lib.Ether(
			lib.EtherDst("52:54:00:6b:ff:a5"),
			lib.EtherSrc("52:54:00:6b:ff:a1"),
		),
		lib.IPv4(
			lib.IPSrc(params.Ipv4Src),
			lib.IPDst("153.153.153.153"),
			lib.IPTTL(64),
			lib.IPId(1),
		),
		lib.TCP(
			lib.TCPSport(params.TcpSport),
			lib.TCPDport(2048),
			lib.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create009_nat64statelessSendPacket2 generates packets
func create009_nat64statelessSendPacket2(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-7 (using helper)
	paramsList := []create009_nat64statelessSendPacket2Params{
		{Ipv4Src: "102.102.102.102", Ipv4Chksum: 31440, TcpSport: 80, TcpChksum: 34707},
		{Ipv4Src: "102.102.102.102", Ipv4Chksum: 31440, TcpSport: 443, TcpChksum: 34344},
		{Ipv4Src: "102.102.102.103", Ipv4Chksum: 31439, TcpSport: 80, TcpChksum: 34706},
		{Ipv4Src: "102.102.102.103", Ipv4Chksum: 31439, TcpSport: 443, TcpChksum: 34343},
		{Ipv4Src: "102.102.102.102", Ipv4Chksum: 31440, TcpSport: 80, TcpChksum: 34707},
		{Ipv4Src: "102.102.102.102", Ipv4Chksum: 31440, TcpSport: 443, TcpChksum: 34344},
		{Ipv4Src: "102.102.102.103", Ipv4Chksum: 31439, TcpSport: 80, TcpChksum: 34706},
		{Ipv4Src: "102.102.102.103", Ipv4Chksum: 31439, TcpSport: 443, TcpChksum: 34343},
	}

	for _, params := range paramsList {
		packets = append(packets, create009_nat64statelessSendPacket2Helper(t, params))
	}

	return packets
}

// reading from file 009_nat64stateless/002-expect.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6666.80 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6571 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6666.443 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6406 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6667.80 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6570 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6667.443 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6405 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6666.80 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6571 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6666.443 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6406 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6667.80 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6570 (correct), seq 0, win 8192, length 0
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 78: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header TCP (6) payload length: 20) 5555:5555:5555:5555:5555:5555:6666:6667.443 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa.2048: Flags [S], cksum 0x6405 (correct), seq 0, win 8192, length 0
// create009_nat64statelessExpectPacket2Params holds varying parameters for packet generation
type create009_nat64statelessExpectPacket2Params struct {
	Ipv6Src   string
	TcpSport  uint16
	TcpChksum uint16
}

// create009_nat64statelessExpectPacket2Helper generates a single packet with varying parameters
func create009_nat64statelessExpectPacket2Helper(t *testing.T, params create009_nat64statelessExpectPacket2Params) gopacket.Packet {
	pkt, err := lib.NewPacket(nil,
		lib.Ether(
			lib.EtherDst("52:54:00:6b:ff:a1"),
			lib.EtherSrc("52:54:00:6b:ff:a5"),
		),
		lib.IPv6(
			lib.IPv6Src(params.Ipv6Src),
			lib.IPv6Dst("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
			lib.IPv6HopLimit(63),
		),
		lib.TCP(
			lib.TCPSport(params.TcpSport),
			lib.TCPDport(2048),
			lib.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create009_nat64statelessExpectPacket2 generates packets
func create009_nat64statelessExpectPacket2(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-7 (using helper)
	paramsList := []create009_nat64statelessExpectPacket2Params{
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6666", TcpSport: 80, TcpChksum: 25969},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6666", TcpSport: 443, TcpChksum: 25606},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6667", TcpSport: 80, TcpChksum: 25968},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6667", TcpSport: 443, TcpChksum: 25605},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6666", TcpSport: 80, TcpChksum: 25969},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6666", TcpSport: 443, TcpChksum: 25606},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6667", TcpSport: 80, TcpChksum: 25968},
		{Ipv6Src: "5555:5555:5555:5555:5555:5555:6666:6667", TcpSport: 443, TcpChksum: 25605},
	}

	for _, params := range paramsList {
		packets = append(packets, create009_nat64statelessExpectPacket2Helper(t, params))
	}

	return packets
}

// reading from file 009_nat64stateless/007-send.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 87: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header ICMPv6 (58) payload length: 29) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa > 5555:5555:5555:5555:5555:5555:6666:6666: [icmp6 sum ok] ICMP6, echo request, id 4660, seq 34661
// 1970-01-01 03:00:00.000000 00:00:00:00:00:01 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 75: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 64, next-header ICMPv6 (58) payload length: 17) aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa > 5555:5555:5555:5555:5555:5555:6666:6666: [icmp6 sum ok] ICMP6, echo reply, id 22136, seq 17185
// create009_nat64statelessSendPacket3 generates packets
func create009_nat64statelessSendPacket3(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a5"),
				lib.EtherSrc("52:54:00:6b:ff:a1"),
			),
			lib.IPv6(
				lib.IPv6Src("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
				lib.IPv6Dst("5555:5555:5555:5555:5555:5555:6666:6666"),
				lib.IPv6HopLimit(64),
				lib.IPv6NextHeader(layers.IPProtocol(58)),
				lib.IPv6PayloadLength(29),
			),
			lib.ICMPv6EchoRequest(
				lib.ICMPv6Id(4660),
				lib.ICMPv6Seq(34661),
				lib.ICMPv6Checksum(33522),
			),
			lib.Raw(
				[]byte("du hast vyacheslavich"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	// Packet 1
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a5"),
				lib.EtherSrc("52:54:00:6b:ff:a1"),
			),
			lib.IPv6(
				lib.IPv6Src("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
				lib.IPv6Dst("5555:5555:5555:5555:5555:5555:6666:6666"),
				lib.IPv6HopLimit(64),
				lib.IPv6NextHeader(layers.IPProtocol(58)),
				lib.IPv6PayloadLength(17),
			),
			lib.ICMPv6EchoReply(
				lib.ICMPv6Id(22136),
				lib.ICMPv6Seq(17185),
				lib.ICMPv6Checksum(55443),
			),
			lib.Raw(
				[]byte("vitalya 2"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}

// reading from file 009_nat64stateless/007-expect.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 67: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto ICMP (1), length 49)
//
//	153.153.153.153 > 102.102.102.102: ICMP echo request, id 4660, seq 34661, length 29
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 55: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 0, offset 0, flags [none], proto ICMP (1), length 37)
//
//	153.153.153.153 > 102.102.102.102: ICMP echo reply, id 22136, seq 17185, length 17
//
// create009_nat64statelessExpectPacket3 generates packets
func create009_nat64statelessExpectPacket3(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a1"),
				lib.EtherSrc("52:54:00:6b:ff:a5"),
			),
			lib.IPv4(
				lib.IPSrc("153.153.153.153"),
				lib.IPDst("102.102.102.102"),
				lib.IPTTL(63),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(0),
				lib.IPv4Length(49),
				lib.IPv4ChecksumRaw(31693),
			),
			lib.ICMP(
				lib.ICMPTypeCode(8, 0),
				lib.ICMPId(4660),
				lib.ICMPSeq(34661),
				lib.ICMPChecksum(7532),
			),
			lib.Raw(
				[]byte("du hast vyacheslavich"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	// Packet 1
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a1"),
				lib.EtherSrc("52:54:00:6b:ff:a5"),
			),
			lib.IPv4(
				lib.IPSrc("153.153.153.153"),
				lib.IPDst("102.102.102.102"),
				lib.IPTTL(63),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(0),
				lib.IPv4Length(37),
				lib.IPv4ChecksumRaw(31705),
			),
			lib.ICMP(
				lib.ICMPTypeCode(0, 0),
				lib.ICMPId(22136),
				lib.ICMPSeq(17185),
				lib.ICMPChecksum(31745),
			),
			lib.Raw(
				[]byte("vitalya 2"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}

// reading from file 009_nat64stateless/008-send.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 67: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto ICMP (1), length 49)
//
//	102.102.102.102 > 153.153.153.153: ICMP echo reply, id 4660, seq 34661, length 29
//
// 1970-01-01 03:00:00.000000 00:00:00:00:00:02 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 55: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto ICMP (1), length 37)
//
//	102.102.102.102 > 153.153.153.153: ICMP echo request, id 22136, seq 17185, length 17
//
// create009_nat64statelessSendPacket4 generates packets
func create009_nat64statelessSendPacket4(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a5"),
				lib.EtherSrc("52:54:00:6b:ff:a1"),
			),
			lib.IPv4(
				lib.IPSrc("102.102.102.102"),
				lib.IPDst("153.153.153.153"),
				lib.IPTTL(64),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(1),
				lib.IPv4Length(49),
				lib.IPv4ChecksumRaw(31436),
			),
			lib.ICMP(
				lib.ICMPTypeCode(0, 0),
				lib.ICMPId(4660),
				lib.ICMPSeq(34661),
				lib.ICMPChecksum(9580),
			),
			lib.Raw(
				[]byte("du hast vyacheslavich"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	// Packet 1
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a5"),
				lib.EtherSrc("52:54:00:6b:ff:a1"),
			),
			lib.IPv4(
				lib.IPSrc("102.102.102.102"),
				lib.IPDst("153.153.153.153"),
				lib.IPTTL(64),
				lib.IPProto(layers.IPProtocol(1)),
				lib.IPId(1),
				lib.IPv4Length(37),
				lib.IPv4ChecksumRaw(31448),
			),
			lib.ICMP(
				lib.ICMPTypeCode(8, 0),
				lib.ICMPId(22136),
				lib.ICMPSeq(17185),
				lib.ICMPChecksum(29697),
			),
			lib.Raw(
				[]byte("vitalya 2"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}

// reading from file 009_nat64stateless/008-expect.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 87: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header ICMPv6 (58) payload length: 29) 5555:5555:5555:5555:5555:5555:6666:6666 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa: [icmp6 sum ok] ICMP6, echo reply, id 4660, seq 34661
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:11:11:11, ethertype 802.1Q (0x8100), length 75: vlan 100, p 0, ethertype IPv6 (0x86dd), (hlim 63, next-header ICMPv6 (58) payload length: 17) 5555:5555:5555:5555:5555:5555:6666:6666 > aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa: [icmp6 sum ok] ICMP6, echo request, id 22136, seq 17185
// create009_nat64statelessExpectPacket4 generates packets
func create009_nat64statelessExpectPacket4(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packet 0
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a1"),
				lib.EtherSrc("52:54:00:6b:ff:a5"),
			),
			lib.IPv6(
				lib.IPv6Src("5555:5555:5555:5555:5555:5555:6666:6666"),
				lib.IPv6Dst("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
				lib.IPv6HopLimit(63),
				lib.IPv6NextHeader(layers.IPProtocol(58)),
				lib.IPv6PayloadLength(29),
			),
			lib.ICMPv6EchoReply(
				lib.ICMPv6Id(4660),
				lib.ICMPv6Seq(34661),
				lib.ICMPv6Checksum(33266),
			),
			lib.Raw(
				[]byte("du hast vyacheslavich"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	// Packet 1
	{
		pkt, err := lib.NewPacket(nil,
			lib.Ether(
				lib.EtherDst("52:54:00:6b:ff:a1"),
				lib.EtherSrc("52:54:00:6b:ff:a5"),
			),
			lib.IPv6(
				lib.IPv6Src("5555:5555:5555:5555:5555:5555:6666:6666"),
				lib.IPv6Dst("aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa"),
				lib.IPv6HopLimit(63),
				lib.IPv6NextHeader(layers.IPProtocol(58)),
				lib.IPv6PayloadLength(17),
			),
			lib.ICMPv6EchoRequest(
				lib.ICMPv6Id(22136),
				lib.ICMPv6Seq(17185),
				lib.ICMPv6Checksum(55699),
			),
			lib.Raw(
				[]byte("vitalya 2"),
			),
		)

		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	return packets
}
