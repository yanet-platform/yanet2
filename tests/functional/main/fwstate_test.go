package functional

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
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

// expectedIPv6Sources are the IPv6 entry sources in forward listing order.
var expectedIPv6Sources = []string{
	"[2001:db8:1::10]:20000",
	"[2001:db8:1::10]:20001",
	"[2001:db8:1::10]:20002",
}

// expectedIPv4Sources returns the sources of expectedEntries in listing order.
func expectedIPv4Sources() []string {
	sources := make([]string, 0, len(expectedEntries))
	for _, entry := range expectedEntries {
		sources = append(sources, entry.src)
	}
	return sources
}

// decodeJSONLines decodes the CLI's newline-delimited JSON output.
func decodeJSONLines[T any](t *testing.T, output string) []T {
	t.Helper()
	var decoded []T
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		require.NoError(t, json.Unmarshal([]byte(line), &item))
		decoded = append(decoded, item)
	}
	return decoded
}

// jsonEntrySources returns each listed entry's source in output order.
func jsonEntrySources(t *testing.T, output string) []string {
	t.Helper()
	type sourceEntry struct {
		Key struct {
			SrcAddr string `json:"src_addr"`
			SrcPort int    `json:"src_port"`
		} `json:"key"`
	}
	entries := decodeJSONLines[sourceEntry](t, output)
	sources := make([]string, 0, len(entries))
	for _, entry := range entries {
		sources = append(sources, net.JoinHostPort(entry.Key.SrcAddr, strconv.Itoa(entry.Key.SrcPort)))
	}
	return sources
}

// listedEndpoints parses the source and destination of every row of the
// human-formatted listing.
//
// ParseAddrPort rejects an IPv6 endpoint whose address is not bracketed,
// so parsing keeps that rendering covered without pinning the column text.
func listedEndpoints(t *testing.T, output string) (srcs, dsts []netip.AddrPort) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue // header row
		}
		src, err := netip.ParseAddrPort(fields[1])
		require.NoError(t, err, "source endpoint %q should parse", fields[1])
		dst, err := netip.ParseAddrPort(fields[2])
		require.NoError(t, err, "destination endpoint %q should parse", fields[2])
		srcs = append(srcs, src)
		dsts = append(dsts, dst)
	}
	return srcs, dsts
}

// TestFWStateMapListEntries covers the fwstate-map introspection surface:
// forward/backward/paginated entry dumps and stats, read from the map
// objects by name via the map CLI.
func TestFWStateMapListEntries(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testFWStateMapListEntries(t, fw)
	})
}

func testFWStateMapListEntries(t *testing.T, fw *framework.TestFramework) {
	const mapV4, mapV6 = "fwstate0-v4", "fwstate0-v6"

	// 1. Create the fwstate-map objects the config below links by name.
	fw.Run("Create_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWStateMap + " create --name " + mapV4 + " --kind v4 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWStateMap + " create --name " + mapV6 + " --kind v6 --index-size 1024 --extra-bucket-count 64",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate-map creation failed")
	})

	// 2. Configure fwstate module with map links and sync settings.
	fw.Run("Configure_fwstate", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWState + " update --name fwstate0" +
				" --map-name-v4 " + mapV4 +
				" --map-name-v6 " + mapV6 +
				" --src-addr 2001:db8::100" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	// 3. Wire the ACL config to the same state maps so the dataplane
	// modules are active.
	fw.Run("Wire_acl_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --name acl_fw" +
				" /mnt/yanet2/tests/functional/testdata/acl+fwstate.yaml" +
				" --map-name-v4 " + mapV4 + " --map-name-v6 " + mapV6,
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_fw,fwstate:fwstate0,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "acl state map wiring failed")
	})

	// 4. Inject packets to create firewall state entries.
	fw.Run("Create_state_entries", func(fw *framework.TestFramework, t *testing.T) {
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

	// 5. Forward listing on the v4 map: verify exact entries.
	fw.Run("Forward_listing", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV4 + " --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "map entries forward failed")
		t.Log("Forward listing output:\n", output)

		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "should contain source %s", e.src)
			require.Contains(t, output, e.dst, "should contain destination %s", e.dst)
		}
		require.Contains(t, output, "TCP")
		require.Contains(t, output, "-S--|----")
	})

	// 6. Forward listing with JSON: parse and verify key fields.
	fw.Run("Forward_listing_json", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV4 + " --batch 100 --direction forward --include-expired --format json",
		)
		require.NoError(t, err, "map entries forward json failed")
		t.Log("JSON output:\n", output)

		const tcpProto = 6
		// flags is the raw fw_state_flags_u byte: low nibble tracks the
		// forward direction, high nibble the backward direction, with bits
		// 0x01 FIN, 0x02 SYN, 0x04 RST, 0x08 ACK. SYN-only forward is 0x02.
		const flagsSYNForward = 2

		// The CLI serializes the FwStateEntry proto message as-is.
		type jsonEntry struct {
			Idx int `json:"idx"`
			Key struct {
				Proto   int    `json:"proto"`
				SrcPort int    `json:"src_port"`
				DstPort int    `json:"dst_port"`
				SrcAddr string `json:"src_addr"`
				DstAddr string `json:"dst_addr"`
			} `json:"key"`
			Value struct {
				External        bool `json:"external"`
				Flags           int  `json:"flags"`
				PacketsForward  int  `json:"packets_forward"`
				PacketsBackward int  `json:"packets_backward"`
			} `json:"value"`
		}

		entries := decodeJSONLines[jsonEntry](t, output)
		require.Len(t, entries, 3, "should have exactly 3 JSON entries")

		for i, e := range entries {
			require.Equal(t, i, e.Idx, "entry %d idx", i)
			require.Equal(t, tcpProto, e.Key.Proto, "entry %d proto should be TCP", i)
			require.Equal(t, 10000+i, e.Key.SrcPort, "entry %d src_port", i)
			require.Equal(t, 80, e.Key.DstPort, "entry %d dst_port", i)
			require.Equal(t, fmt.Sprintf("192.0.2.%d", 10+i), e.Key.SrcAddr, "entry %d src_addr", i)
			require.Equal(t, "192.0.3.1", e.Key.DstAddr, "entry %d dst_addr", i)
			require.False(t, e.Value.External, "entry %d should not be external", i)
			require.Equal(t, flagsSYNForward, e.Value.Flags, "entry %d flags should be SYN forward only", i)
			require.Equal(t, 1, e.Value.PacketsForward, "entry %d packets_forward", i)
			require.Equal(t, 0, e.Value.PacketsBackward, "entry %d packets_backward", i)
		}
	})

	// 7. Backward listing from last entry: verify all entries present.
	fw.Run("Backward_listing", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV4 + " --batch 100 --direction backward --index 4294967295 --include-expired",
		)
		require.NoError(t, err, "map entries backward failed")
		require.NotEmpty(t, output, "backward listing returned empty output")
		t.Log("Backward listing output:\n", output)

		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "backward should include %s", e.src)
		}
	})

	// 8. Pagination: read with batch=1, verify all entries are still returned.
	fw.Run("Pagination", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV4 + " --batch 1 --direction forward --include-expired",
		)
		require.NoError(t, err, "map entries with batch=1 failed")
		t.Log("Pagination output:\n", output)

		// CLI loops through batches; all 3 entries should appear.
		for _, e := range expectedEntries {
			require.Contains(t, output, e.src, "pagination should include %s", e.src)
		}
	})

	// 9. Map not found: request entries from a non-existent map.
	fw.Run("Map_not_found", func(fw *framework.TestFramework, t *testing.T) {
		_, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name nonexistent --batch 10",
		)
		require.Error(t, err, "should fail for non-existent map")
	})

	// 10. Stats: verify total_elements matches the number of injected entries.
	fw.Run("Stats_after_entries", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " stats --name " + mapV4,
		)
		require.NoError(t, err, "map stats command failed")
		t.Log("Stats output:\n", output)

		var stats struct {
			Stats struct {
				TotalElements int `json:"total_elements"`
				IndexSize     int `json:"index_size"`
				LayerCount    int `json:"layer_count"`
			} `json:"stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats), "stats should be valid JSON")
		require.Equal(t, 1024, stats.Stats.IndexSize)
		require.Equal(t, 1, stats.Stats.LayerCount)
		require.Equal(t, 3, stats.Stats.TotalElements, "should have exactly 3 state entries")
	})

	// === IPv6 ===

	// 11. Create IPv6 state entries via CreateState.
	fw.Run("IPv6_create_state", func(fw *framework.TestFramework, t *testing.T) {
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
			require.Len(t, out, 2, "CreateState should produce 2 packets (original + sync)")
		}
	})

	// 12. IPv6 forward listing: verify entries were created.
	fw.Run("IPv6_forward_listing", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV6 + " --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "IPv6 map entries forward failed")
		t.Log("IPv6 forward listing output:\n", output)

		src := netip.MustParseAddr("2001:db8:1::10")
		dst := netip.AddrPortFrom(netip.MustParseAddr("2001:db8:2::1"), 80)

		srcs, dsts := listedEndpoints(t, output)
		require.Equal(t, []netip.AddrPort{
			netip.AddrPortFrom(src, 20000),
			netip.AddrPortFrom(src, 20001),
			netip.AddrPortFrom(src, 20002),
		}, srcs)
		require.Equal(t, []netip.AddrPort{dst, dst, dst}, dsts)
		require.Contains(t, output, "TCP")
	})

	// 13. IPv6 JSON listing keeps the family's sources in order.
	fw.Run("IPv6_listing_json", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name " + mapV6 + " --batch 100 --direction forward --include-expired --format json",
		)
		require.NoError(t, err, "IPv6 map entries json failed")
		require.Equal(t, expectedIPv6Sources, jsonEntrySources(t, output))
	})

	// 14. IPv6 stats: verify total_elements.
	fw.Run("IPv6_stats", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " stats --name " + mapV6,
		)
		require.NoError(t, err, "map stats command failed")
		t.Log("Stats output:\n", output)

		var stats struct {
			Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats), "stats should be valid JSON")
		require.Equal(t, 3, stats.Stats.TotalElements, "should have exactly 3 IPv6 state entries")
	})
}

// TestFWStateCheckState covers the fwstate state lifecycle end to end:
// state entries created by CreateState are matched by CheckState on
// return traffic, and unknown flows are dropped, for both IPv4 and IPv6.
func TestFWStateCheckState(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testFWStateCheckState(t, fw)
	})
}

func testFWStateCheckState(t *testing.T, fw *framework.TestFramework) {

	// 1. Create the fwstate-map objects the configs below link by name.
	fw.Run("Create_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWStateMap + " create --name fwstate0-v4 --kind v4 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWStateMap + " create --name fwstate0-v6 --kind v6 --index-size 1024 --extra-bucket-count 64",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate-map creation failed")
	})

	// 2. Configure fwstate module with map links and sync settings.
	fw.Run("Configure_fwstate", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWState + " update --name fwstate0" +
				" --map-name-v4 fwstate0-v4" +
				" --map-name-v6 fwstate0-v6" +
				" --src-addr 2001:db8::100" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	// 3. Wire the ACL config to the same state maps so the dataplane
	// modules are active.
	fw.Run("Wire_acl_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --name acl_fw" +
				" /mnt/yanet2/tests/functional/testdata/acl+fwstate.yaml" +
				" --map-name-v4 fwstate0-v4 --map-name-v6 fwstate0-v6",
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_fw,fwstate:fwstate0,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "acl state map wiring failed")
	})

	// 4. Inject packets to create firewall state entries.
	fw.Run("Create_state_entries", func(fw *framework.TestFramework, t *testing.T) {
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

	// 5. CheckState: return traffic passes through ACL because forward state exists.
	fw.Run("CheckState_return_traffic", func(fw *framework.TestFramework, t *testing.T) {
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

	// 6. CheckState: packet with no matching state is dropped.
	fw.Run("CheckState_no_state_dropped", func(fw *framework.TestFramework, t *testing.T) {
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

	// === IPv6 tests ===

	// 7. Create IPv6 state entries via CreateState.
	fw.Run("IPv6_create_state", func(fw *framework.TestFramework, t *testing.T) {
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

	// 8. IPv6 CheckState: return traffic passes because forward state exists.
	fw.Run("IPv6_CheckState_return_traffic", func(fw *framework.TestFramework, t *testing.T) {
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

	// 9. IPv6 CheckState: packet with no matching state is dropped.
	fw.Run("IPv6_CheckState_no_state_dropped", func(fw *framework.TestFramework, t *testing.T) {
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
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testFWStateUDPEndianness(t, fw)
	})
}

func testFWStateUDPEndianness(t *testing.T, fw *framework.TestFramework) {

	// 1. Create the fwstate-map objects and configure fwstate.
	fw.Run("Configure_fwstate", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWStateMap + " create --name fwstate_udp-v4 --kind v4 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWStateMap + " create --name fwstate_udp-v6 --kind v6 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWState + " update --name fwstate_udp" +
				" --map-name-v4 fwstate_udp-v4" +
				" --map-name-v6 fwstate_udp-v6" +
				" --src-addr 2001:db8::100" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	fw.Run("Wire_acl_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --name acl_udp" +
				" /mnt/yanet2/tests/functional/testdata/acl+fwstate.yaml" +
				" --map-name-v4 fwstate_udp-v4 --map-name-v6 fwstate_udp-v6",
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
	fw.Run("Create_UDP_state", func(fw *framework.TestFramework, t *testing.T) {
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

	// 3. Verify state was created with correct ports via map listing.
	fw.Run("Verify_UDP_state_entries", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name fwstate_udp-v4 --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "map entries forward failed")
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
	fw.Run("CheckState_UDP_return_traffic", func(fw *framework.TestFramework, t *testing.T) {
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

	// 4. Control test: TCP should work (TCP ports are correctly converted).
	fw.Run("Create_TCP_state_control", func(fw *framework.TestFramework, t *testing.T) {
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

	fw.Run("CheckState_TCP_return_traffic_control", func(fw *framework.TestFramework, t *testing.T) {
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
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testFWStateExternalSyncFrame(t, fw)
	})
}

func testFWStateExternalSyncFrame(t *testing.T, fw *framework.TestFramework) {

	// 1. Create the fwstate-map objects and configure the fwstate module.
	fw.Run("Configure_fwstate", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIFWStateMap + " create --name fwstate_ext-v4 --kind v4 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWStateMap + " create --name fwstate_ext-v6 --kind v6 --index-size 1024 --extra-bucket-count 64",
			framework.CLIFWState + " update --name fwstate_ext" +
				" --map-name-v4 fwstate_ext-v4" +
				" --map-name-v6 fwstate_ext-v6" +
				" --src-addr 2001:db8::100" +
				" --dst-addr-multicast ff02::1" +
				" --port-multicast 9999" +
				" --tcp 120s --tcp-syn 60s --tcp-syn-ack 60s --tcp-fin 60s" +
				" --udp 30s --default 16s",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "fwstate configuration failed")
	})

	// 2. Wire the ACL config to the same state maps.
	fw.Run("Wire_acl_state_maps", func(fw *framework.TestFramework, t *testing.T) {
		commands := []string{
			framework.CLIACL + " update --name acl_ext" +
				" /mnt/yanet2/tests/functional/testdata/acl+fwstate.yaml" +
				" --map-name-v4 fwstate_ext-v4 --map-name-v6 fwstate_ext-v6",
			framework.CLIFunction + " update --name=test --chains ch0:2=acl:acl_ext,fwstate:fwstate_ext,route:route0",
			framework.CLIPipeline + " update --name=test --functions test",
		}
		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "acl state map wiring failed")
	})

	// 3. Verify no state entries exist initially.
	fw.Run("Verify_empty_state", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " stats --name fwstate_ext-v4",
		)
		require.NoError(t, err, "map stats command failed")
		t.Log("Initial stats:\n", output)

		var stats struct {
			Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats))
		require.Equal(t, 0, stats.Stats.TotalElements, "should start with 0 state entries")
	})

	// 4. Send an external sync frame and verify it is dropped.
	// The sync frame carries a TCP SYN state for 192.0.2.77:5000 -> 192.0.3.1:80,
	// inside the subnets the rules file's CheckState rule covers, so the
	// return traffic below reaches CHECK_STATE.
	fw.Run("Send_external_sync_frame", func(fw *framework.TestFramework, t *testing.T) {
		syncFrame := buildFWStateSyncFrame(
			net.IPv4(192, 0, 2, 77), // src IP
			net.IPv4(192, 0, 3, 1),  // dst IP
			5000,                    // src port
			80,                      // dst port
			6,                       // proto: TCP
			0,                       // fib: 0 = forward (INGRESS)
			0x02,                    // flags: SYN
			4,                       // addr_type: IPv4
		)
		pkt := buildExternalSyncPacket(syncFrame, nil)

		// The external sync frame should be dropped by fwstate after processing.
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		_ = err
		require.Nil(t, out, "external sync frame must be dropped by fwstate (not forwarded)")
	})

	// 5. Verify that the external sync frame created a state entry.
	fw.Run("Verify_state_created", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name fwstate_ext-v4 --batch 100 --direction forward --include-expired",
		)
		require.NoError(t, err, "map entries forward failed")
		t.Log("Entries after external sync:\n", output)

		require.Contains(t, output, "192.0.2.77:5000", "should contain source from external sync frame")
		require.Contains(t, output, "192.0.3.1:80", "should contain destination from external sync frame")
		require.Contains(t, output, "TCP", "should be TCP protocol")
	})

	// 6. Verify the state entry is marked as external via JSON listing.
	fw.Run("Verify_state_is_external", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " entries --name fwstate_ext-v4 --batch 100 --direction forward --include-expired --format json",
		)
		require.NoError(t, err, "map entries json failed")
		t.Log("JSON entries:\n", output)

		const tcpProto = 6

		type jsonEntry struct {
			Key struct {
				Proto   int    `json:"proto"`
				SrcPort int    `json:"src_port"`
				DstPort int    `json:"dst_port"`
				SrcAddr string `json:"src_addr"`
				DstAddr string `json:"dst_addr"`
			} `json:"key"`
			Value struct {
				External bool `json:"external"`
			} `json:"value"`
		}

		entries := decodeJSONLines[jsonEntry](t, output)
		require.Len(t, entries, 1, "should have exactly 1 state entry from external sync")
		e := entries[0]
		require.Equal(t, "192.0.2.77", e.Key.SrcAddr, "src_addr should match sync frame")
		require.Equal(t, "192.0.3.1", e.Key.DstAddr, "dst_addr should match sync frame")
		require.Equal(t, 5000, e.Key.SrcPort, "src_port should match sync frame")
		require.Equal(t, 80, e.Key.DstPort, "dst_port should match sync frame")
		require.Equal(t, tcpProto, e.Key.Proto, "proto should be TCP")
		require.True(t, e.Value.External, "entry should be marked external")
	})

	// 7. Verify stats show exactly 1 entry.
	fw.Run("Verify_stats", func(fw *framework.TestFramework, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWStateMap + " stats --name fwstate_ext-v4",
		)
		require.NoError(t, err, "map stats command failed")
		t.Log("Stats after external sync:\n", output)

		var stats struct {
			Stats struct {
				TotalElements int `json:"total_elements"`
			} `json:"stats"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &stats))
		require.Equal(t, 1, stats.Stats.TotalElements, "should have exactly 1 state entry")
	})

	// 8. Verify the external sync frame created state: return traffic for
	// the synced flow passes through ACL via CheckState.
	fw.Run("CheckState_external_return_traffic", func(fw *framework.TestFramework, t *testing.T) {
		pg := NewPacketGenerator()

		// Reverse of the synced flow: 192.0.3.1:80 -> 192.0.2.77:5000.
		// Forwarded only when the external state landed in the map.
		pkt := pg.TCP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 77),
			80, 5000,
			true, true, false, false, // SYN+ACK
			[]byte("external return"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		require.NoError(t, err, "external return packet should not error")
		require.NotNil(t, out, "return packet should be forwarded by CheckState over the external state")
	})

	// 9. Control: a flow the external frame did not sync is dropped.
	fw.Run("CheckState_unsynced_dropped", func(fw *framework.TestFramework, t *testing.T) {
		pg := NewPacketGenerator()

		pkt := pg.TCP(
			net.IPv4(192, 0, 3, 1),
			net.IPv4(192, 0, 2, 78),
			80, 55555,
			true, true, false, false, // SYN+ACK
			[]byte("unsynced flow"),
		)
		_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
		_ = err
		require.Nil(t, out, "packet with no matching state should be dropped")
	})
}
