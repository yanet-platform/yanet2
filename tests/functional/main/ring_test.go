package functional

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ringInfo is one ring as the ring CLI prints it in JSON.
type ringInfo struct {
	Name         string `json:"name"`
	Capacity     uint64 `json:"capacity"`
	PublishBatch uint32 `json:"publish_batch"`
}

// ringCLI runs the ring CLI with the given arguments in the guest.
func ringCLI(fw *framework.TestFramework, args string) (string, error) {
	return fw.ExecuteCommand(framework.CLIRing + " " + args)
}

// listRings returns every registered ring from the JSON list output.
func listRings(t *testing.T, fw *framework.TestFramework) []ringInfo {
	t.Helper()
	output, err := ringCLI(fw, "list --format json")
	require.NoError(t, err, "ring list failed")
	var rings []ringInfo
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &rings),
		"ring list must be a JSON array: %q", output)
	return rings
}

// showRing returns one ring from the JSON show output.
func showRing(t *testing.T, fw *framework.TestFramework, name string) ringInfo {
	t.Helper()
	output, err := ringCLI(fw, "show --format json --name "+name)
	require.NoError(t, err, "ring show failed")
	var response struct {
		Ring *ringInfo `json:"ring"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &response),
		"ring show must be a JSON object: %q", output)
	require.NotNil(t, response.Ring, "ring show must carry the ring")
	return *response.Ring
}

// findRing returns the listed ring with the given name.
//
// The second result is false when no listed ring has that name.
func findRing(rings []ringInfo, name string) (ringInfo, bool) {
	for _, ring := range rings {
		if ring.Name == name {
			return ring, true
		}
	}
	return ringInfo{}, false
}

// Test_RingCLI_Lifecycle checks that the ring CLI drives the ring service
// that a running pdump module hosts.
//
// A created ring appears in list and show with its capacity and publish
// batch. A refused create reports its gRPC code in JSON and leaves the
// registry unchanged. A deleted ring is gone from list and show.
func Test_RingCLI_Lifecycle(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testRingCLILifecycle(t, fw)
	})
}

func testRingCLILifecycle(t *testing.T, fw *framework.TestFramework) {
	const (
		ringName     = "ring-tfn0"
		capacity     = uint64(64 << 10)
		publishBatch = uint32(32)
	)

	// Remove the test ring from the shared VM after a failure.
	//
	// A failed step may leave the ring behind. Later tests need an empty
	// registry. A delete of a ring that does not exist just fails, and that
	// is fine here.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		_, _ = ringCLI(fw, "delete --name "+ringName)
	})

	fw.Run("Create_lists_and_shows_ring", func(fw *framework.TestFramework, t *testing.T) {
		_, listed := findRing(listRings(t, fw), ringName)
		require.False(t, listed, "ring must not exist before create")

		_, err := ringCLI(fw, fmt.Sprintf(
			"create --name %s --capacity %d --publish-batch %d",
			ringName, capacity, publishBatch,
		))
		require.NoError(t, err, "ring create failed")

		want := ringInfo{Name: ringName, Capacity: capacity, PublishBatch: publishBatch}
		ring, listed := findRing(listRings(t, fw), ringName)
		require.True(t, listed, "created ring must be listed")
		require.Equal(t, want, ring)
		require.Equal(t, want, showRing(t, fw, ringName))
	})

	fw.Run("Duplicate_create_changes_nothing", func(fw *framework.TestFramework, t *testing.T) {
		before := listRings(t, fw)

		output, err := ringCLI(fw, fmt.Sprintf("--format json create --name %s --capacity %d", ringName, capacity))
		require.Error(t, err, "duplicate create must exit non-zero")
		var failure struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &failure),
			"duplicate create must report a JSON error: %q", output)
		require.False(t, failure.OK)
		require.Equal(t, "AlreadyExists", failure.Error.Code)
		require.ElementsMatch(t, before, listRings(t, fw), "duplicate create must not change the registry")
	})

	fw.Run("Delete_removes_ring", func(fw *framework.TestFramework, t *testing.T) {
		_, err := ringCLI(fw, "delete --name "+ringName)
		require.NoError(t, err, "ring delete failed")

		_, listed := findRing(listRings(t, fw), ringName)
		require.False(t, listed, "deleted ring must not be listed")
		_, err = ringCLI(fw, "show --name "+ringName)
		require.Error(t, err, "show of a deleted ring must exit non-zero")
	})
}

// pdumpCLI runs the pdump CLI with the given arguments in the guest.
func pdumpCLI(fw *framework.TestFramework, args string) (string, error) {
	return fw.ExecuteCommand(framework.CLIPdump + " " + args)
}

// cliErrorEnvelope is the JSON body a yanet-cli command prints on a refused
// request with --format json.
type cliErrorEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// readGuestFileBase64 returns the raw bytes of a guest file, base64-round-
// tripped off the serial console.
//
// Binary content, such as a pcap capture, would not survive the console's
// text handling otherwise. The console swallows an unbroken multi-hundred-
// byte line, so this keeps base64's default wrapped output and strips all
// whitespace rather than requesting a single unwrapped line.
func readGuestFileBase64(t *testing.T, fw *framework.TestFramework, path string) []byte {
	t.Helper()
	output, err := fw.ExecuteCommand("base64 " + path)
	require.NoError(t, err, "reading %s failed", path)
	compact := strings.Join(strings.Fields(output), "")
	data, err := base64.StdEncoding.DecodeString(compact)
	require.NoError(t, err, "decoding base64 content of %s failed", path)
	return data
}

// readPcapRecords parses a pcap capture and returns its header snaplen
// together with each record's captured bytes.
func readPcapRecords(t *testing.T, data []byte) (snaplen uint32, records [][]byte) {
	t.Helper()
	reader, err := pcapgo.NewReader(bytes.NewReader(data))
	require.NoError(t, err, "parsing the pcap header failed")

	for {
		record, _, err := reader.ReadPacketData()
		if err != nil {
			require.ErrorIs(t, err, io.EOF, "reading pcap record %d failed", len(records))
			break
		}
		records = append(records, record)
	}
	return reader.Snaplen(), records
}

// captureMarker returns the payload that identifies the i-th packet a
// capture test sends, so the packet that carries it and the record that
// must later be found for it share one definition.
func captureMarker(i int) string {
	return fmt.Sprintf("pdump ring capture %d", i)
}

// Test_PdumpRingCapture checks that a pdump config captures the traffic it
// observes into a named ring object, driven end to end through the CLI.
//
// A config cannot be created without naming a ring, and naming an absent
// one is rejected too; neither attempt leaves a config behind. Once bound
// and wired into the traffic path, the config captures every packet that
// crosses it into the ring, readable back as a PCAP stream whose header
// snaplen is the fixed value regardless of the configured capture snaplen.
// The ring stays referenced while the config exists, so deleting it is
// refused; removing the config first clears the way. It does not run in
// parallel, since it rewrites the function device 01:00.0's input
// pipeline uses.
func Test_PdumpRingCapture(t *testing.T) {
	withBootedVM(t, func(fw *framework.TestFramework) {
		testPdumpRingCapture(t, fw)
	})
}

func testPdumpRingCapture(t *testing.T, fw *framework.TestFramework) {
	const (
		ringName        = "ring-tfn-pdump0"
		missingRingName = ringName + "-missing"
		configName      = "pdump-tfn0"
		capacity        = uint64(128 << 10)
		publishBatch    = uint32(32)
		capturePath     = "/mnt/config/pdump-tfn0-capture.pcap"
		captureCount    = 3

		// The function "test" backs device 01:00.0's input pipeline (see
		// CommonConfigCommands). The chain below restores its baseline;
		// the one after puts the pdump config first so it sees every
		// packet before route0 decides where it goes next.
		defaultTestChain = "chain2:1=forward:forward0,route:route0"
		capturedChain    = "chain-pdump:1=pdump:" + configName + ",route:route0"
	)

	// Undo everything this test changes, on every path: out of the traffic
	// path, then the config, then the ring, in the order that unblocks
	// each delete.
	//
	// The passing path already does this through its own steps below, so
	// here it just repeats harmlessly against already-restored state. A
	// step that failed partway, though, needs exactly this to leave the
	// shared VM usable for what runs next.
	t.Cleanup(func() {
		_, _ = fw.ExecuteCommand(framework.CLIFunction + " update --name=test --chains " + defaultTestChain)
		_, _ = pdumpCLI(fw, "delete --name "+configName)
		_, _ = ringCLI(fw, "delete --name "+ringName)
	})

	fw.Run("Create_ring", func(fw *framework.TestFramework, t *testing.T) {
		_, err := ringCLI(fw, fmt.Sprintf(
			"create --name %s --capacity %d --publish-batch %d",
			ringName, capacity, publishBatch,
		))
		require.NoError(t, err, "ring create failed")
	})

	fw.Run("Create_without_ring_name_fails", func(fw *framework.TestFramework, t *testing.T) {
		output, err := pdumpCLI(fw, "--format json set --name "+configName)
		require.Error(t, err, "a create without a ring name must be refused")
		var failure cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &failure),
			"refused create must report a JSON error: %q", output)
		require.Equal(t, "InvalidArgument", failure.Error.Code)

		_, err = pdumpCLI(fw, "show --name "+configName)
		require.Error(t, err, "a refused create must not leave a config behind")
	})

	fw.Run("Create_with_absent_ring_fails", func(fw *framework.TestFramework, t *testing.T) {
		output, err := pdumpCLI(fw, fmt.Sprintf(
			"--format json set --name %s --ring-name %s", configName, missingRingName,
		))
		require.Error(t, err, "a create naming an absent ring must be refused")
		var failure cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &failure),
			"refused create must report a JSON error: %q", output)
		require.Equal(t, "NotFound", failure.Error.Code)

		_, err = pdumpCLI(fw, "show --name "+configName)
		require.Error(t, err, "a refused create must not leave a config behind")
	})

	fw.Run("Create_bound_to_ring", func(fw *framework.TestFramework, t *testing.T) {
		// Pdump without a filter captures every packet the chain sees,
		// not only this test's own traffic. The filter keeps the ring to
		// the UDP flow the capture step sends, so unrelated packets
		// cannot fill the --num budget ahead of it.
		_, err := pdumpCLI(fw, fmt.Sprintf(
			`set --name %s --ring-name %s --filter "udp and dst host 192.0.3.1"`,
			configName, ringName,
		))
		require.NoError(t, err, "pdump set failed")

		output, err := pdumpCLI(fw, "--format json show --name "+configName)
		require.NoError(t, err, "pdump show failed")
		var response struct {
			Config *struct {
				RingName string `json:"ring_name"`
			} `json:"config"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &response),
			"pdump show must report a JSON config: %q", output)
		require.NotNil(t, response.Config, "pdump show must carry the config")
		require.Equal(t, ringName, response.Config.RingName)
	})

	fw.Run("Wire_into_traffic_path", func(fw *framework.TestFramework, t *testing.T) {
		_, err := fw.ExecuteCommand(framework.CLIFunction + " update --name=test --chains " + capturedChain)
		require.NoError(t, err, "wiring the pdump config into the test function failed")
	})

	fw.Run("Capture_matching_packets", func(fw *framework.TestFramework, t *testing.T) {
		// A fresh stream starts at the ring's current write position, so
		// a packet sent before the reader subscribes is invisible to it.
		//
		// There is no signal for "now subscribed", so the same marker set
		// is resent on a short interval until the background reader hits
		// its target or a deadline passes. Because the markers repeat
		// with a period equal to their own count, any run of that many
		// consecutive records is the complete set regardless of which
		// resend the reader happened to catch.
		markers := make([]string, captureCount)
		for i := range captureCount {
			markers[i] = captureMarker(i)
		}

		exitPath := capturePath + ".exit"
		// The trailing ": " keeps the launched job from being the last
		// token on the line: the console wraps every command with its
		// own trailing ";" to capture a status, and a bare "&" directly
		// against that is a shell syntax error.
		bgCmd := fmt.Sprintf(
			"rm -f %s; nohup sh -c '%s read --name %s --dump-format pcap --num %d -o %s; echo $? > %s' "+
				">/dev/null 2>&1 & :",
			exitPath, framework.CLIPdump, configName, captureCount, capturePath, exitPath,
		)
		_, err := fw.ExecuteCommand(bgCmd)
		require.NoError(t, err, "starting the background pdump read failed")
		t.Cleanup(func() {
			_, _ = fw.ExecuteCommand(fmt.Sprintf("pkill -f '%s read --name %s'", framework.CLIPdump, configName))
		})

		pg := NewPacketGenerator()
		exited := false
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			for i, marker := range markers {
				pkt := pg.UDP(
					net.ParseIP("192.0.2.2"),
					net.ParseIP("192.0.3.1"),
					uint16(20000+i), 600,
					[]byte(marker),
				)

				_, out, err := fw.SendPacketAndParse(0, 0, pkt, 200*time.Millisecond)
				require.NoError(t, err, "packet %d must reach the dataplane", i)
				require.NotNil(t, out, "packet %d must come back through route0", i)
			}

			if _, err := fw.ExecuteCommand("test -f " + exitPath); err == nil {
				exited = true
				break
			}
		}
		require.True(t, exited, "pdump read must capture %d records before the deadline", captureCount)

		exitCode, err := fw.ExecuteCommand("cat " + exitPath)
		require.NoError(t, err, "reading the background read's exit code failed")
		require.Equal(t, "0", strings.TrimSpace(exitCode), "pdump read must exit cleanly")

		snaplen, records := readPcapRecords(t, readGuestFileBase64(t, fw, capturePath))
		require.Equal(t, uint32(65535), snaplen, "pcap header snaplen must be fixed regardless of capture snaplen")
		require.Len(t, records, captureCount, "every sent packet must be captured")

		seen := make([]bool, captureCount)
		for _, record := range records {
			found := false
			for i, marker := range markers {
				if !seen[i] && bytes.Contains(record, []byte(marker)) {
					seen[i] = true
					found = true
					break
				}
			}
			require.True(t, found, "captured record must be one of the sent packets: % x", record)
		}
		for i, ok := range seen {
			require.True(t, ok, "packet %d must have been captured", i)
		}
	})

	fw.Run("Delete_bound_ring_is_refused", func(fw *framework.TestFramework, t *testing.T) {
		output, err := ringCLI(fw, "--format json delete --name "+ringName)
		require.Error(t, err, "delete of a ring linked by a pdump config must be refused")
		var failure cliErrorEnvelope
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(output)), &failure),
			"refused delete must report a JSON error: %q", output)
		require.Equal(t, "FailedPrecondition", failure.Error.Code)

		_, listed := findRing(listRings(t, fw), ringName)
		require.True(t, listed, "a refused delete must leave the ring in place")
	})

	fw.Run("Restore_pipeline_and_delete_config", func(fw *framework.TestFramework, t *testing.T) {
		// The config must stop being referenced by the chain before it can
		// be deleted: a config still linked by a live chain is refused,
		// independently of the ring check above.
		_, err := fw.ExecuteCommand(framework.CLIFunction + " update --name=test --chains " + defaultTestChain)
		require.NoError(t, err, "restoring the test function failed")

		_, err = pdumpCLI(fw, "delete --name "+configName)
		require.NoError(t, err, "pdump delete failed")
	})

	fw.Run("Delete_ring_succeeds", func(fw *framework.TestFramework, t *testing.T) {
		_, err := ringCLI(fw, "delete --name "+ringName)
		require.NoError(t, err, "ring delete failed")

		_, listed := findRing(listRings(t, fw), ringName)
		require.False(t, listed, "deleted ring must not be listed")
	})
}
