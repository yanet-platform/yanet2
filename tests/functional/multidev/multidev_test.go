package multidev

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// routeCfgName is the route module config these tests drive. It matches the
// name the baseline configuration wires into the "test" function chain.
const routeCfgName = "route0"

// splitPrefix is routed out secondDevice while everything else keeps the
// baseline nexthop on 01:00.0, so one send proves both the presence on the
// intended interface and the absence on the other.
const splitPrefix = "198.51.100.0/24"

// captureTimeout bounds each interface's idle-based capture. The captures run
// concurrently, so this is the cost of the whole call rather than per
// interface.
const captureTimeout = 200 * time.Millisecond

// nexthop names the device a prefix is routed out of.
type nexthop struct {
	prefix string
	device string
}

// fibConfig builds a route FIB YAML mapping each prefix to a nexthop on the
// named device.
//
// Order matters and the argument is a slice for that reason: the FIB installs
// each entry as an address range and a later range overwrites an earlier one it
// covers, so a default route listed after a more specific prefix erases it.
// List least specific first.
//
// The MAC pair is swapped relative to the framework's canonical SrcMAC/DstMAC
// so egressing packets carry the host's expected destination MAC, which is
// also what the capture path filters on.
func fibConfig(t *testing.T, nexthops []nexthop) string {
	t.Helper()

	type fibRangeYAML struct {
		Start string `yaml:"start"`
		End   string `yaml:"end"`
	}
	type fibNexthopYAML struct {
		DstMAC string `yaml:"dst_mac"`
		SrcMAC string `yaml:"src_mac"`
		Device string `yaml:"device"`
	}
	type fibEntryYAML struct {
		Range    fibRangeYAML     `yaml:"range"`
		Nexthops []fibNexthopYAML `yaml:"nexthops"`
	}
	type fibConfigYAML struct {
		Entries []fibEntryYAML `yaml:"entries"`
	}

	cfg := fibConfigYAML{Entries: make([]fibEntryYAML, 0, len(nexthops))}
	for _, hop := range nexthops {
		start, end, err := framework.PrefixRange(hop.prefix)
		require.NoError(t, err, "failed to parse prefix %q", hop.prefix)

		cfg.Entries = append(cfg.Entries, fibEntryYAML{
			Range: fibRangeYAML{Start: start, End: end},
			Nexthops: []fibNexthopYAML{{
				DstMAC: framework.SrcMAC,
				SrcMAC: framework.DstMAC,
				Device: hop.device,
			}},
		})
	}

	body, err := yaml.Marshal(&cfg)
	require.NoError(t, err, "failed to marshal FIB config")

	return string(body)
}

// configureSplitRouting registers secondDevice with the control plane and
// installs a FIB that sends splitPrefix out of it while the default route
// keeps using 01:00.0.
//
// Registering the device is not optional: the dataplane declares 02:00.0, but
// until it carries pipelines the round has no execution context for it and
// drops everything addressed there.
func configureSplitRouting(t *testing.T, fw *framework.TestFramework) {
	t.Helper()

	fib := fibConfig(t, []nexthop{
		{prefix: "0.0.0.0/0", device: "01:00.0"},
		{prefix: splitPrefix, device: secondDevice},
	})
	require.NoError(t, fw.CreateConfigFile("multidev-fib.yaml", fib),
		"failed to write FIB config")

	commands := []string{
		framework.CLIDevicePlain + " update --name=" + secondDevice +
			" --input bootstrap:1 --output dummy:1",
		framework.CLIRoute + " fib update --name=" + routeCfgName +
			" --rules /mnt/config/multidev-fib.yaml",
	}
	_, err := fw.ExecuteCommands(commands...)
	require.NoError(t, err, "failed to configure split routing")
}

// TestMultiDeviceRoute verifies that a nexthop on the second device egresses
// there and nowhere else, observed from a single send.
//
// Both interfaces are captured per send, so the "absent from the other
// interface" half is a real observation rather than the second send the
// one-output Send methods would need. Two destinations sent through the same
// pipeline separate a working per-device demux from a dataplane that simply
// sends everything one way.
func TestMultiDeviceRoute(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		fw.Run("Configure_Split_Routing", func(fw *framework.TestFramework, t *testing.T) {
			configureSplitRouting(t, fw)
		})

		fw.Run("Route_To_Second_Device", func(fw *framework.TestFramework, t *testing.T) {
			packet := framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.100"),
				net.ParseIP("198.51.100.10"),
				[]byte("second device"),
				nil,
			)

			captured, err := fw.SendPacketAndParseByIface(
				iface0, []int{iface0, iface1}, packet, captureTimeout,
			)
			require.NoError(t, err, "failed to send and capture")

			require.Len(t, captured[iface1], 1, "packet must egress on the second device")
			require.Equal(t, "198.51.100.10", captured[iface1][0].DstIP.String())
			require.Empty(t, captured[iface0], "packet must not also egress on 01:00.0")
		})

		fw.Run("Default_Route_Stays_On_First_Device", func(fw *framework.TestFramework, t *testing.T) {
			packet := framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.100"),
				net.ParseIP("203.0.113.200"),
				[]byte("first device"),
				nil,
			)

			captured, err := fw.SendPacketAndParseByIface(
				iface0, []int{iface0, iface1}, packet, captureTimeout,
			)
			require.NoError(t, err, "failed to send and capture")

			require.Len(t, captured[iface0], 1, "packet must egress on 01:00.0")
			require.Equal(t, "203.0.113.200", captured[iface0][0].DstIP.String())
			require.Empty(t, captured[iface1], "packet must not leak to the second device")
		})
	})
}

// TestMultiDeviceCaptureRejectsDuplicateIface guards the capture API's one
// reader per socket rule: ReceiveAllPackets does not hold the client's
// connection mutex, so two goroutines reading one socket would interleave
// reads of the same stream and split frames between them.
func TestMultiDeviceCaptureRejectsDuplicateIface(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		fw.Run("Duplicate_Output_Iface", func(fw *framework.TestFramework, t *testing.T) {
			packet := framework.CreateTCPIPv4Packet(
				net.ParseIP("192.0.2.100"),
				net.ParseIP("203.0.113.200"),
				nil,
				nil,
			)

			_, err := fw.SendPacketAndCaptureByIface(
				iface0, []int{iface0, iface0}, packet, captureTimeout,
			)
			require.ErrorContains(t, err, "listed more than once")
		})
	})
}
