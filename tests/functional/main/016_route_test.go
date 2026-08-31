package functional

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestTest_016_route verifies that TCP packets to distinct destinations are
// forwarded via the default route with rewritten MACs and a decremented TTL.
//
// Migrated from yanet1 autotest 016_route.
func TestTest_016_route(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		require.NotNil(t, fw, "Global framework should be initialized")

		fw.Run("Step_001_Configure_Routes", func(fw *framework.TestFramework, t *testing.T) {
			// IPv4 routes configuration
			// Original autotest.yaml step:
			// ipv4Update:
			//   - "0.0.0.0/0 -> 200.0.0.1"
			applyFIB(t, fw, "route0", "step001", "0.0.0.0/0", "::/0")
		})

		fw.Run("Step_001_Test_Packet", func(fw *framework.TestFramework, t *testing.T) {
			// Test case: 001-send.pcap -> 001-expect.pcap
			sendPackets := create016_routeSendPacket1(t)
			require.NotNil(t, sendPackets)
			require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

			expectedPackets := create016_routeExpectPacket1(t)
			require.NotNil(t, expectedPackets)

			// Get socket client
			client, err := fw.GetSocketClient(0)
			require.NoError(t, err, "Failed to get socket client")
			defer client.Close()
			require.NoError(t, client.Connect(), "Failed to connect to socket")

			budget := framework.NewReplyBudget()
			var receivedPackets []gopacket.Packet
			for idx, pkt := range sendPackets {
				packetBytes := pkt.Data()

				// Send packet and wait for a reply. A missing reply is
				// tolerated here and surfaces below as a count mismatch.
				responseData, err := client.SendAndReceivePacket(budget, packetBytes, "")
				require.NoError(t, err, "Failed to send packet %d", idx)
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

				diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(), framework.CmpStdOpts...)
				require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)
			}
		})
	})
}

// reading from file 016_route/001-send.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.0.0.0.80: Flags [S], cksum 0xd0c1 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.1.0.0.80: Flags [S], cksum 0xd0c0 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.2.0.0.80: Flags [S], cksum 0xd0bf (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.3.0.0.80: Flags [S], cksum 0xd0be (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.4.0.0.80: Flags [S], cksum 0xd0bd (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.5.0.0.80: Flags [S], cksum 0xd0bc (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.6.0.0.80: Flags [S], cksum 0xd0bb (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.7.0.0.80: Flags [S], cksum 0xd0ba (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.8.0.0.80: Flags [S], cksum 0xd0b9 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:00:00:11:11:11 > 00:11:22:33:44:55, ethertype 802.1Q (0x8100), length 58: vlan 100, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 64, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.9.0.0.80: Flags [S], cksum 0xd0b8 (correct), seq 0, win 8192, length 0
//
// create016_routeSendPacket1Params holds varying parameters for packet generation
type create016_routeSendPacket1Params struct {
	Ipv4Chksum uint16
	Ipv4Dst    string
	TcpChksum  uint16
}

// create016_routeSendPacket1Helper generates a single packet with varying parameters
func create016_routeSendPacket1Helper(t *testing.T, params create016_routeSendPacket1Params) gopacket.Packet {
	pkt, err := framework.NewPacket(nil,
		framework.Ether(
			framework.EtherDst("52:54:00:6b:ff:a5"),
			framework.EtherSrc("52:54:00:6b:ff:a1"),
		),
		framework.IPv4(
			framework.IPSrc("222.222.222.222"),
			framework.IPDst(params.Ipv4Dst),
			framework.IPTTL(64),
			framework.IPId(1),
		),
		framework.TCP(
			framework.TCPSport(20),
			framework.TCPDport(80),
			framework.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create016_routeSendPacket1 generates packets
func create016_routeSendPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-9 (using helper)
	paramsList := []create016_routeSendPacket1Params{
		{Ipv4Chksum: 48146, Ipv4Dst: "1.0.0.0", TcpChksum: 53441},
		{Ipv4Chksum: 48145, Ipv4Dst: "1.1.0.0", TcpChksum: 53440},
		{Ipv4Chksum: 48144, Ipv4Dst: "1.2.0.0", TcpChksum: 53439},
		{Ipv4Chksum: 48143, Ipv4Dst: "1.3.0.0", TcpChksum: 53438},
		{Ipv4Chksum: 48142, Ipv4Dst: "1.4.0.0", TcpChksum: 53437},
		{Ipv4Chksum: 48141, Ipv4Dst: "1.5.0.0", TcpChksum: 53436},
		{Ipv4Chksum: 48140, Ipv4Dst: "1.6.0.0", TcpChksum: 53435},
		{Ipv4Chksum: 48139, Ipv4Dst: "1.7.0.0", TcpChksum: 53434},
		{Ipv4Chksum: 48138, Ipv4Dst: "1.8.0.0", TcpChksum: 53433},
		{Ipv4Chksum: 48137, Ipv4Dst: "1.9.0.0", TcpChksum: 53432},
	}

	for _, params := range paramsList {
		packets = append(packets, create016_routeSendPacket1Helper(t, params))
	}

	return packets
}

// reading from file 016_route/001-expect.pcap, link-type EN10MB (Ethernet)
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.0.0.0.80: Flags [S], cksum 0xd0c1 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.1.0.0.80: Flags [S], cksum 0xd0c0 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.2.0.0.80: Flags [S], cksum 0xd0bf (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.3.0.0.80: Flags [S], cksum 0xd0be (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.4.0.0.80: Flags [S], cksum 0xd0bd (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.5.0.0.80: Flags [S], cksum 0xd0bc (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.6.0.0.80: Flags [S], cksum 0xd0bb (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.7.0.0.80: Flags [S], cksum 0xd0ba (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.8.0.0.80: Flags [S], cksum 0xd0b9 (correct), seq 0, win 8192, length 0
//
// 1970-01-01 03:00:00.000000 00:11:22:33:44:55 > 00:00:00:22:22:22, ethertype 802.1Q (0x8100), length 58: vlan 200, p 0, ethertype IPv4 (0x0800), (tos 0x0, ttl 63, id 1, offset 0, flags [none], proto TCP (6), length 40)
//
//	222.222.222.222.20 > 1.9.0.0.80: Flags [S], cksum 0xd0b8 (correct), seq 0, win 8192, length 0
//
// create016_routeExpectPacket1Params holds varying parameters for packet generation
type create016_routeExpectPacket1Params struct {
	Ipv4Dst    string
	Ipv4Chksum uint16
	TcpChksum  uint16
}

// create016_routeExpectPacket1Helper generates a single packet with varying parameters
func create016_routeExpectPacket1Helper(t *testing.T, params create016_routeExpectPacket1Params) gopacket.Packet {
	pkt, err := framework.NewPacket(nil,
		framework.Ether(
			framework.EtherDst("52:54:00:6b:ff:a1"),
			framework.EtherSrc("52:54:00:6b:ff:a5"),
		),
		framework.IPv4(
			framework.IPSrc("222.222.222.222"),
			framework.IPDst(params.Ipv4Dst),
			framework.IPTTL(63),
			framework.IPId(1),
		),
		framework.TCP(
			framework.TCPSport(20),
			framework.TCPDport(80),
			framework.TCPFlags("S"),
		),
	)
	require.NoError(t, err)
	return pkt
}

// create016_routeExpectPacket1 generates packets
func create016_routeExpectPacket1(t *testing.T) []gopacket.Packet {
	var packets []gopacket.Packet

	// Packets 0-9 (using helper)
	paramsList := []create016_routeExpectPacket1Params{
		{Ipv4Dst: "1.0.0.0", Ipv4Chksum: 48402, TcpChksum: 53441},
		{Ipv4Dst: "1.1.0.0", Ipv4Chksum: 48401, TcpChksum: 53440},
		{Ipv4Dst: "1.2.0.0", Ipv4Chksum: 48400, TcpChksum: 53439},
		{Ipv4Dst: "1.3.0.0", Ipv4Chksum: 48399, TcpChksum: 53438},
		{Ipv4Dst: "1.4.0.0", Ipv4Chksum: 48398, TcpChksum: 53437},
		{Ipv4Dst: "1.5.0.0", Ipv4Chksum: 48397, TcpChksum: 53436},
		{Ipv4Dst: "1.6.0.0", Ipv4Chksum: 48396, TcpChksum: 53435},
		{Ipv4Dst: "1.7.0.0", Ipv4Chksum: 48395, TcpChksum: 53434},
		{Ipv4Dst: "1.8.0.0", Ipv4Chksum: 48394, TcpChksum: 53433},
		{Ipv4Dst: "1.9.0.0", Ipv4Chksum: 48393, TcpChksum: 53432},
	}

	for _, params := range paramsList {
		packets = append(packets, create016_routeExpectPacket1Helper(t, params))
	}

	return packets
}
