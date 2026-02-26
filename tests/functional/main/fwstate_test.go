package functional

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

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
	{"192.0.2.10:10000", "192.0.3.1:80", "TCP", "0x02"},
	{"192.0.2.11:10001", "192.0.3.1:80", "TCP", "0x02"},
	{"192.0.2.12:10002", "192.0.3.1:80", "TCP", "0x02"},
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
			_, _, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
			_ = err // CreateState does not forward the original packet
		}

		time.Sleep(500 * time.Millisecond)
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
		require.Contains(t, output, "0x02")
	})

	// 5. Forward listing with JSON: parse and verify key fields.
	fw.Run("Forward_listing_json", func(fw *framework.F, t *testing.T) {
		output, err := fw.ExecuteCommand(
			framework.CLIFWState + " entries --cfg fwstate0 --batch 100 --direction forward --include-expired --json",
		)
		require.NoError(t, err, "list-entries forward json failed")
		t.Log("JSON output:\n", output)

		type jsonKey struct {
			Proto   int `json:"proto"`
			SrcPort int `json:"src_port"`
			DstPort int `json:"dst_port"`
			SrcAddr struct {
				Bytes []int `json:"bytes"`
			} `json:"src_addr"`
			DstAddr struct {
				Bytes []int `json:"bytes"`
			} `json:"dst_addr"`
		}
		type jsonEntry struct {
			Key   jsonKey `json:"key"`
			Idx   int     `json:"idx"`
			Value struct {
				ProtocolType   int  `json:"protocol_type"`
				Flags          int  `json:"flags"`
				PacketsForward int  `json:"packets_forward"`
				External       bool `json:"external"`
			} `json:"value"`
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
			require.Equal(t, 6, e.Key.Proto, "entry %d proto should be TCP(6)", i)
			require.Equal(t, 10000+i, e.Key.SrcPort, "entry %d src_port", i)
			require.Equal(t, 80, e.Key.DstPort, "entry %d dst_port", i)
			require.Equal(t, []int{192, 0, 2, 10 + i}, e.Key.SrcAddr.Bytes, "entry %d src_addr", i)
			require.Equal(t, []int{192, 0, 3, 1}, e.Key.DstAddr.Bytes, "entry %d dst_addr", i)
			require.Equal(t, 6, e.Value.ProtocolType, "entry %d protocol_type", i)
			require.Equal(t, 2, e.Value.Flags, "entry %d flags should be SYN(0x02)", i)
			require.Equal(t, 1, e.Value.PacketsForward, "entry %d packets_forward", i)
			require.False(t, e.Value.External, "entry %d should not be external", i)
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
			_, _, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
			_ = err
		}

		time.Sleep(500 * time.Millisecond)
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
