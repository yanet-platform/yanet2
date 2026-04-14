package functional

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// expectedEntries are the 3 state entries we expect after injecting packets.
// Order matches forward listing (ascending index).
var expectedEntries = []struct {
	src   string
	dst   string
	proto string
	flags string
}{
	{"192.0.2.10:10000", "192.0.3.1:80", "TCP", "-S--|----"},
	{"192.0.2.11:10001", "192.0.3.1:80", "TCP", "-S--|----"},
	{"192.0.2.12:10002", "192.0.3.1:80", "TCP", "-S--|----"},
}

func TestFWStateListEntries(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Test framework must be initialized")

	// 1. Configure fwstate module with maps and sync settings.
	fw.Run("Configure_fwstate", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIFWState + " update --cfg fwstate0" +
				" --index-size 1024" +
				" --extra-bucket-count 64" +
				" --src-addr 2001:db8::100" +
				" --dst-ether 33:33:00:00:00:01" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	// 2. Link fwstate to an ACL config so the dataplane module is active.
	fw.Run("Link_fwstate_to_acl", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --cfg acl_fw --rules /mnt/yanet2/acl+fwstate.yaml",
			framework.CLIFWState + " link --cfg fwstate0 --acl acl_fw",
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_fw,fwstate:fwstate0,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate link configuration failed")
	})

	// 3. Inject packets to create firewall state entries.
	fw.Run("Create_state_entries", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		for i := range 3 {
			srcPort := uint16(10000 + i)
			pkt := pg.TCP(
				net.IPv4(192, 0, 2, byte(10+i)),
				net.IPv4(192, 0, 3, 1),
				srcPort, 80,
				true, false, false, false, // SYN
				[]byte("state entry"),
			)
			out, err := fw.SendPacketAndParseAll(0, 0, pkt, 200*time.Millisecond)
			require.NoError(t, err, "CreateState should not error")
			require.NotEmpty(t, out, "CreateState should forward packets")
			// CreateState produces 2 packets: original + sync packet
			require.Len(t, out, 2, "CreateState should produce 2 packets (original + sync)")
		}
	})

	// 4. Forward listing: verify exact entries.
	fw.Run("Forward_listing", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "list-entries forward failed")
		t.Log("Forward listing output:\n", output)

		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "should contain source %s", e.src)
			require.Contains(t, output, e.dst, "should contain destination %s", e.dst)
		}
		require.Contains(t, output, "TCP")
		require.Contains(t, output, "-S--|----")
	})

	// 5. Forward listing with JSON: parse and verify key fields.
	fw.Run("Forward_listing_json", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --batch 100 --direction forward --include-expired --json",
		)
		require.NoError(t, err, "list-entries forward json failed")
		t.Log("JSON output:\n", output)

		type jsonEntry struct {
			Idx     int    `json:"idx"`
			SrcPort int    `json:"src_port"`
			DstPort int    `json:"dst_port"`
			SrcAddr string `json:"src_addr"`
			DstAddr string `json:"dst_addr"`
			Proto   string `json:"proto"`
			Origin  string `json:"origin"`
			Flags   struct {
				Src []string `json:"src"`
				Dst []string `json:"dst"`
			} `json:"flags"`
			Packets struct {
				Src int `json:"src"`
				Dst int `json:"dst"`
			} `json:"packets"`
		}

		var entries []jsonEntry
		for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var e jsonEntry
			require.NoError(t, json.Unmarshal([]byte(line), &e))
			entries = append(entries, e)
		}

		require.Len(t, entries, 3, "should have exactly 3 JSON entries")

		for i, e := range entries {
			require.Equal(t, i, e.Idx, "entry %d idx", i)
			require.Equal(t, "TCP", e.Proto, "entry %d proto should be TCP", i)
			require.Equal(t, 10000+i, e.SrcPort, "entry %d src_port", i)
			require.Equal(t, 80, e.DstPort, "entry %d dst_port", i)
			require.Equal(t, fmt.Sprintf("192.0.2.%d", 10+i), e.SrcAddr, "entry %d src_addr", i)
			require.Equal(t, "192.0.3.1", e.DstAddr, "entry %d dst_addr", i)
			require.Equal(t, "local", e.Origin, "entry %d origin should be local", i)
			require.Equal(t, []string{"SYN"}, e.Flags.Src, "entry %d flags.src should be [SYN]", i)
			require.Empty(t, e.Flags.Dst, "entry %d flags.dst should be empty", i)
			require.Equal(t, 1, e.Packets.Src, "entry %d packets.src", i)
			require.Equal(t, 0, e.Packets.Dst, "entry %d packets.dst", i)
		}
	})

	// 6. Backward listing from last entry: verify all entries present.
	fw.Run("Backward_listing", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --batch 100 --direction backward --index 4294967295 --include-expired",
		)
		require.NoError(t, err, "list-entries backward failed")
		t.Log("Backward listing output:\n", output)

		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "backward should include %s", e.src)
		}
	})

	// 7. Pagination: read with batch=1, verify all entries are still returned.
	fw.Run("Pagination", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --batch 1 --direction forward --include-expired",
		)
		require.NoError(t, err, "list-entries with batch=1 failed")
		t.Log("Pagination output:\n", output)

		// CLI loops through batches; all 3 entries should appear.
		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "pagination should include %s", e.src)
		}
	})

	// 8. Config not found: request entries from a non-existent config.
	fw.Run("Config_not_found", func(fw *framework.F, t *testing.T) {
		_, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg nonexistent --batch 10",
		)
		require.Error(t, err, "should fail for non-existent config")
	})

	// 9. CheckState: return traffic passes through ACL because forward state exists.
	fw.Run("CheckState_return_traffic", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// Send a TCP SYN-ACK from 192.0.3.1:80 -> 192.0.2.10:10000.
		// This is the reverse of the first forward entry.
		// CheckState should find the matching state and forward the packet.
		pkt := pg.TCP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 10),
			80, 10000,
			true, true, false, false, // SYN+ACK
			[]byte("return traffic"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "return packet should not error")
		require.NotNil(t, out, "return packet should be forwarded by CheckState")
	})

	// 10. CheckState: packet with no matching state is dropped.
	fw.Run("CheckState_no_state_dropped", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// Send a TCP packet from 192.0.3.1:80 -> 192.0.2.99:55555.
		// No forward state exists for 192.0.2.99:55555, so CheckState drops it.
		pkt := pg.TCP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 99),
			80, 55555,
			true, true, false, false, // SYN+ACK
			[]byte("unknown traffic"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		_ = err
		require.Nil(t, out, "packet with no matching state should be dropped")
	})

	// 11. Stats: verify total_elements matches the number of injected entries.
	fw.Run("Stats_after_entries", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " stats --cfg fwstate0",
		)
		require.NoError(t, err, "stats command failed")
		t.Log("Stats output:\n", output)

		var stats struct {
			IPv4Stats struct {
				TotalElements int `json:"total_elements"`
				IndexSize     int `json:"index_size"`
				LayerCount    int `json:"layer_count"`
			} `json:"ipv4_stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats), "stats should be valid JSON")
		require.Equal(t, 1024, stats.IPv4Stats.IndexSize)
		require.Equal(t, 1, stats.IPv4Stats.LayerCount)
		require.Equal(t, 3, stats.IPv4Stats.TotalElements, "should have exactly 3 state entries")
	})

	// === IPv6 tests ===

	// 12. Create IPv6 state entries via CreateState.
	fw.Run("IPv6_create_state", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		for i := range 3 {
			srcPort := uint16(20000 + i)
			pkt := pg.TCPv6(
				net.ParseIP("2001:db8:1::10"),
				net.ParseIP("2001:db8:2::1"),
				srcPort, 80,
				true, false, false, false, // SYN
				[]byte("v6 state"),
			)
			out, err := fw.SendPacketAndParseAll(0, 0, pkt, 200*time.Millisecond)
			require.NoError(t, err, "CreateState should not error")
			require.NotEmpty(t, out, "CreateState should forward packets")
			// CreateState produces 2 packets: original + sync packet
			require.Len(t, out, 2, "CreateState should produce 2 packets (original + sync)")
		}
	})

	// 13. IPv6 forward listing: verify entries were created.
	fw.Run("IPv6_forward_listing", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --ipv6 --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "IPv6 list-entries forward failed")
		t.Log("IPv6 forward listing output:\n", output)

		require.Contains(t, output, "2001:db8:1::10:20000")
		require.Contains(t, output, "2001:db8:1::10:20001")
		require.Contains(t, output, "2001:db8:1::10:20002")
		require.Contains(t, output, "2001:db8:2::1:80")
		require.Contains(t, output, "TCP")
	})

	// 14. IPv6 CheckState: return traffic passes because forward state exists.
	fw.Run("IPv6_CheckState_return_traffic", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// Reverse of first IPv6 entry: 2001:db8:2::1:80 -> 2001:db8:1::10:20000
		pkt := pg.TCPv6(
			net.ParseIP("2001:db8:2::1"),
			net.ParseIP("2001:db8:1::10"),
			80, 20000,
			true, true, false, false, // SYN+ACK
			[]byte("v6 return"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "IPv6 return packet should not error")
		require.NotNil(t, out, "IPv6 return packet should be forwarded by CheckState")
	})

	// 15. IPv6 CheckState: packet with no matching state is dropped.
	fw.Run("IPv6_CheckState_no_state_dropped", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// No state for 2001:db8:1::99:55555
		pkt := pg.TCPv6(
			net.ParseIP("2001:db8:2::1"),
			net.ParseIP("2001:db8:1::99"),
			80, 55555,
			true, true, false, false, // SYN+ACK
			[]byte("v6 unknown"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		_ = err
		require.Nil(t, out, "IPv6 packet with no matching state should be dropped")
	})

	// 16. IPv6 stats: verify total_elements.
	fw.Run("IPv6_stats", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " stats --cfg fwstate0",
		)
		require.NoError(t, err, "stats command failed")
		t.Log("Stats output:\n", output)

		var stats struct {
			IPv6Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"ipv6_stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats), "stats should be valid JSON")
		require.Equal(t, 3, stats.IPv6Stats.TotalElements, "should have exactly 3 IPv6 state entries")
	})
}

// TestFWStateUDPEndianness verifies that UDP state entries created via
// CreateState can be matched by CheckState on return traffic.
//
// This is a regression test for a bug in fwstate_fill_sync_frame() where
// UDP ports were stored in network byte order (big-endian) instead of host
// byte order (little-endian) in the sync frame. TCP ports were correctly
// converted with rte_be_to_cpu_16(), but UDP ports were copied directly
// from the packet header without conversion.
//
// The test creates UDP forward state via CreateState (which internally
// crafts a sync frame and processes it), then sends return traffic that
// should match via CheckState. If the endianness bug is present, the
// return traffic will be dropped because the stored ports won't match
// the lookup key.
func TestFWStateUDPEndianness(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Test framework must be initialized")

	// 1. Configure fwstate + ACL (reuse existing config from TestFWStateListEntries)
	fw.Run("Configure_fwstate", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIFWState + " update --cfg fwstate_udp" +
				" --index-size 1024" +
				" --extra-bucket-count 64" +
				" --src-addr 2001:db8::100" +
				" --dst-ether 33:33:00:00:00:01" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	fw.Run("Link_fwstate_to_acl", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --cfg acl_udp --rules /mnt/yanet2/acl+fwstate.yaml",
			framework.CLIFWState + " link --cfg fwstate_udp --acl acl_udp",
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_udp,fwstate:fwstate_udp,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate link configuration failed")
	})

	// 2. Create UDP forward state via CreateState.
	// Use asymmetric ports where byte-swap matters:
	// Port 12345 = 0x3039, byte-swapped = 0x3930 = 14640
	// If the endianness bug exists, the state will be stored with
	// swapped port bytes and CheckState will fail to match.
	fw.Run("Create_UDP_state", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		pkt := pg.UDP(
			net.IPv4(192, 0, 2, 10),
			net.IPv4(192, 0, 3, 1),
			12345, 80,
			[]byte("udp forward"),
		)
		_, output, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "CreateState should not error")
		require.NotNil(t, output, "CreateState should forward the original packet")
	})

	// 3. Verify state was created with correct ports via entries listing.
	fw.Run("Verify_UDP_state_entries", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate_udp --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "list-entries forward failed")
		t.Log("UDP entries output:\n", output)

		// The entry should show host-order ports (12345, 80), not byte-swapped
		require.Contains(t, output, "192.0.2.10:12345", "should contain correct source port in host byte order")
		require.Contains(t, output, "192.0.3.1:80", "should contain correct destination port")
		require.Contains(t, output, "UDP", "should be UDP protocol")
	})

	// 4. CheckState: return UDP traffic should pass because forward state exists.
	// This is the critical test — if the endianness bug is present, the return
	// traffic will be dropped because the stored ports (in wrong byte order)
	// won't match the lookup key (in correct host byte order).
	fw.Run("CheckState_UDP_return_traffic", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// Send return UDP packet: 192.0.3.1:80 -> 192.0.2.10:12345
		// This is the reverse of the forward entry.
		pkt := pg.UDP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 10),
			80, 12345,
			[]byte("udp return traffic"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "UDP return packet should not error")
		require.NotNil(t, out, "UDP return packet should be forwarded by CheckState (endianness bug if nil)")
	})

	// 5. Control test: TCP should work (TCP ports are correctly converted).
	fw.Run("Create_TCP_state_control", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		pkt := pg.TCP(
			net.IPv4(192, 0, 2, 20),
			net.IPv4(192, 0, 3, 1),
			12345, 80,
			true, false, false, false, // SYN
			[]byte("tcp forward"),
		)
		_, output, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "CreateState should not error")
		require.NotNil(t, output, "CreateState should forward the original packet")
	})

	fw.Run("CheckState_TCP_return_traffic_control", func(fw *framework.F, t *testing.T) {
		pg := NewPacketGenerator()

		// Return TCP: 192.0.3.1:80 -> 192.0.2.20:12345
		pkt := pg.TCP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 20),
			80, 12345,
			true, true, false, false, // SYN+ACK
			[]byte("tcp return"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "TCP return packet should not error")
		require.NotNil(t, out, "TCP return packet should be forwarded by CheckState (control test)")
	})
}

// buildFWStateSyncFrame constructs a raw fw_state_sync_frame (56 bytes)
// matching the C struct layout from lib/fwstate/types.h.
//
// All multi-byte fields except IPv4/IPv6 addresses are little-endian (host byte order on x86).
// IPv4 addresses are stored in network byte order (big-endian).
// IPv6 addresses are in network byte order (big-endian).
func buildFWStateSyncFrame(
	srcIPv4, dstIPv4 net.IP,
	srcPort, dstPort uint16,
	proto uint8,
	fib uint8,
	flags uint8,
	addrType uint8,
) []byte {
	frame := make([]byte, 56)

	switch addrType {
	case 4:
		// IPv4: addresses stored in network byte order (big-endian)
		srcBytes := srcIPv4.To4()
		dstBytes := dstIPv4.To4()
		copy(frame[0:4], dstBytes) // dst_ip in network byte order
		copy(frame[4:8], srcBytes) // src_ip in network byte order
		// IPv6 addresses zeroed (already zero)
	case 6:
		// IPv6: addresses in network byte order (big-endian)
		srcBytes := srcIPv4.To16()
		dstBytes := dstIPv4.To16()
		copy(frame[16:32], dstBytes) // dst_ip6
		copy(frame[32:48], srcBytes) // src_ip6
	}

	// Ports are stored in little-endian (host byte order)
	binary.LittleEndian.PutUint16(frame[8:10], dstPort)
	binary.LittleEndian.PutUint16(frame[10:12], srcPort)

	// Single byte fields
	frame[12] = fib
	frame[13] = proto
	frame[14] = flags
	frame[15] = addrType

	// flow_id6 and extra are zeroed
	return frame
}

// buildExternalSyncPacket constructs a complete external fwstate sync packet
// using gopackets for proper layer serialization:
// Ethernet(VLAN) + VLAN(IPv6) + IPv6 + UDP + fw_state_sync_frame.
//
// The packet mimics what another firewall instance would send to synchronize
// state. The IPv6 source address is non-zero (marking it as external), and
// the destination is the configured multicast address ff02::1 on port 9999.
func buildExternalSyncPacket(syncFrame []byte, srcIPv6 net.IP) []byte {
	if srcIPv6 == nil {
		srcIPv6 = net.ParseIP("2001:db8::200")
	}

	// Ethernet layer with multicast destination MAC
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
		DstMAC:       net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	// VLAN layer
	vlan := &layers.Dot1Q{
		Priority:       0,
		DropEligible:   false,
		VLANIdentifier: 0,
		Type:           layers.EthernetTypeIPv6,
	}

	// IPv6 layer
	ipv6 := &layers.IPv6{
		Version:      6,
		TrafficClass: 0,
		FlowLabel:    0,
		NextHeader:   layers.IPProtocolUDP,
		HopLimit:     64,
		SrcIP:        srcIPv6,
		DstIP:        net.ParseIP("ff02::1"),
	}

	// UDP layer
	udp := &layers.UDP{
		SrcPort: 9999,
		DstPort: 9999,
	}
	udp.SetNetworkLayerForChecksum(ipv6)

	// Serialize all layers
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	layersList := []gopacket.SerializableLayer{
		eth,
		vlan,
		ipv6,
		udp,
		gopacket.Payload(syncFrame),
	}

	if err := gopacket.SerializeLayers(buf, opts, layersList...); err != nil {
		panic(err)
	}

	return buf.Bytes()
}

// TestFWStateExternalSyncFrame verifies that an external fwstate sync frame
// (arriving from the network, simulating another firewall instance) is:
//  1. Allowed through ACL (matching the sync frame allow rule).
//  2. Processed by fwstate to create a new state entry with external=true.
//  3. Dropped by fwstate (external frames are consumed, not forwarded).
//
// This test catches bugs in the fwstate config (e.g., port byte order issues)
// that would only manifest when processing externally-received sync frames,
// as opposed to internally-generated ones where the port is taken from the
// same config used for matching.
func TestFWStateExternalSyncFrame(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Test framework must be initialized")

	// 1. Configure fwstate module.
	fw.Run("Configure_fwstate", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIFWState + " update --cfg fwstate_ext" +
				" --index-size 1024" +
				" --extra-bucket-count 64" +
				" --src-addr 2001:db8::100" +
				" --dst-ether 33:33:00:00:00:01" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	// 2. Link fwstate to ACL with the sync frame allow rule.
	fw.Run("Link_fwstate_to_acl", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --cfg acl_ext --rules /mnt/yanet2/acl+fwstate.yaml",
			framework.CLIFWState + " link --cfg fwstate_ext --acl acl_ext",
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_ext,fwstate:fwstate_ext,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate link configuration failed")
	})

	// 3. Verify no state entries exist initially.
	fw.Run("Verify_empty_state", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " stats --cfg fwstate_ext",
		)
		require.NoError(t, err, "stats command failed")
		t.Log("Initial stats:\n", output)

		var stats struct {
			IPv4Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"ipv4_stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats))
		require.Equal(t, 0, stats.IPv4Stats.TotalElements, "should start with 0 state entries")
	})

	// 4. Send an external sync frame and verify it is dropped.
	// The sync frame carries a TCP SYN state for 10.0.0.1:5000 -> 10.0.0.2:80.
	fw.Run("Send_external_sync_frame", func(fw *framework.F, t *testing.T) {
		syncFrame := buildFWStateSyncFrame(
			net.IPv4(10, 0, 0, 1), // src IP
			net.IPv4(10, 0, 0, 2), // dst IP
			5000,                  // src port
			80,                    // dst port
			6,                     // proto: TCP
			0,                     // fib: 0 = forward (INGRESS)
			0x02,                  // flags: SYN
			4,                     // addr_type: IPv4
		)
		pkt := buildExternalSyncPacket(syncFrame, nil)

		// The external sync frame should be dropped by fwstate after processing.
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		_ = err
		require.Nil(t, out, "external sync frame must be dropped by fwstate (not forwarded)")
	})

	// 5. Verify that the external sync frame created a state entry.
	fw.Run("Verify_state_created", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate_ext --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "list-entries forward failed")
		t.Log("Entries after external sync:\n", output)

		require.Contains(t, output, "10.0.0.1:5000", "should contain source from external sync frame")
		require.Contains(t, output, "10.0.0.2:80", "should contain destination from external sync frame")
		require.Contains(t, output, "TCP", "should be TCP protocol")
	})

	// 6. Verify the state entry is marked as external via JSON listing.
	fw.Run("Verify_state_is_external", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate_ext --batch 100 --direction forward --include-expired --json",
		)
		require.NoError(t, err, "list-entries forward json failed")
		t.Log("JSON entries:\n", output)

		type jsonEntry struct {
			SrcPort int    `json:"src_port"`
			DstPort int    `json:"dst_port"`
			SrcAddr string `json:"src_addr"`
			DstAddr string `json:"dst_addr"`
			Proto   string `json:"proto"`
			Origin  string `json:"origin"`
		}

		var entries []jsonEntry
		for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var e jsonEntry
			require.NoError(t, json.Unmarshal([]byte(line), &e))
			entries = append(entries, e)
		}

		require.Len(t, entries, 1, "should have exactly 1 state entry from external sync")
		e := entries[0]
		require.Equal(t, "10.0.0.1", e.SrcAddr, "src_addr should match sync frame")
		require.Equal(t, "10.0.0.2", e.DstAddr, "dst_addr should match sync frame")
		require.Equal(t, 5000, e.SrcPort, "src_port should match sync frame")
		require.Equal(t, 80, e.DstPort, "dst_port should match sync frame")
		require.Equal(t, "TCP", e.Proto, "proto should be TCP")
		require.Equal(t, "external", e.Origin, "origin should be external")
	})

	// 7. Verify stats show exactly 1 entry.
	fw.Run("Verify_stats", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " stats --cfg fwstate_ext",
		)
		require.NoError(t, err, "stats command failed")
		t.Log("Stats after external sync:\n", output)

		var stats struct {
			IPv4Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"ipv4_stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats))
		require.Equal(t, 1, stats.IPv4Stats.TotalElements, "should have exactly 1 state entry")
	})
}
