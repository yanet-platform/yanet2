//go:build sdk_rust_fixtures

package dataplane_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// prefix4 returns the range of the IPv4 prefix, mapped to the value.
func prefix4(prefix string, value uint32) sdkfixture.Range4 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	first := parsed.Masked().Addr().As4()
	return sdkfixture.Range4{From: first, To: network.LastAddr().As4(), Value: value}
}

// prefix6 returns the range of the IPv6 prefix, mapped to the value.
func prefix6(prefix string, value uint32) sdkfixture.Range6 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	first := parsed.Masked().Addr().As16()
	return sdkfixture.Range6{From: first, To: network.LastAddr().As16(), Value: value}
}

// fibInput returns a FIB of three routes over two devices, eth0 and eth1, with
// two route lists, routes 0 and 2 and then route 1, overlapping IPv4 prefixes
// and twelve IPv6 /64 prefixes under a /32.
//
// The ranges are in insertion order, the broad before the narrow, as the later
// insert wins on an overlap. The twelve /64 prefixes are enough for the IPv6
// table to need more pages than its first chunk holds.
func fibInput() sdkfixture.Fib {
	v6 := []sdkfixture.Range6{prefix6("2001:db8::/32", 0)}
	for subnet := 1; subnet <= 12; subnet++ {
		v6 = append(v6, prefix6(fmt.Sprintf("2001:db8:%x::/64", subnet), 1))
	}
	return sdkfixture.Fib{
		Devices: []string{"eth0", "eth1"},
		Routes: []sdkfixture.FibRoute{
			{DstAddr: [6]byte{0, 0, 0, 0, 0, 1}, Device: 0, Counter: "via_eth0"},
			{DstAddr: [6]byte{0, 0, 0, 0, 0, 2}, Device: 1, Counter: "via_eth1"},
			{DstAddr: [6]byte{0, 0, 0, 0, 0, 3}, Device: 0, Counter: "via_eth0_backup"},
		},
		Lists:   []sdkfixture.RouteList{{Start: 0, Count: 2}, {Start: 2, Count: 1}},
		Indexes: []uint64{0, 2, 1},
		V6:      v6,
		V4: []sdkfixture.Range4{
			prefix4("10.0.0.0/8", 0),
			prefix4("10.1.0.0/16", 1),
			prefix4("10.1.2.0/24", 0),
		},
	}
}

// ipv4Frame returns a UDP frame to the IPv4 address, from a neighbour the FIB
// rewrites.
func ipv4Frame(dst string) []byte {
	frame := make([]byte, 14+20+8)
	copy(frame[0:], []byte{0x02, 0, 0, 0, 0, 0xff})
	copy(frame[6:], []byte{0x02, 0, 0, 0, 0, 0xaa})
	frame[12], frame[13] = 0x08, 0x00
	frame[14] = 0x45
	frame[14+3] = 28
	frame[14+8] = 64
	frame[14+9] = 17
	copy(frame[14+12:], []byte{192, 0, 2, 1})
	copy(frame[14+16:], netip.MustParseAddr(dst).AsSlice())
	frame[14+25] = 8
	return frame
}

// ipv6Frame returns a UDP frame to the IPv6 address, from a neighbour the FIB
// rewrites.
func ipv6Frame(dst string) []byte {
	frame := make([]byte, 14+40+8)
	copy(frame[0:], []byte{0x02, 0, 0, 0, 0, 0xff})
	copy(frame[6:], []byte{0x02, 0, 0, 0, 0, 0xaa})
	frame[12], frame[13] = 0x86, 0xdd
	frame[14] = 0x60
	frame[14+5] = 8
	frame[14+6] = 17
	frame[14+7] = 64
	copy(frame[14+8:], netip.MustParseAddr("2001:db8:1::1").AsSlice())
	copy(frame[14+24:], netip.MustParseAddr(dst).AsSlice())
	frame[14+43] = 8
	return frame
}

// fibCounts is what a FIB bed counts: the packets that reached the output
// pipelines of eth0 and eth1, and the packets counted on the routes via_eth0,
// via_eth0_backup and via_eth1.
type fibCounts struct {
	outputs [2]uint64
	routes  [3]uint64
}

// fibBed is a bed whose port0 forwards by a FIB, and whose devices eth0 and
// eth1 each have a tap on their output pipeline.
type fibBed struct {
	*bed
	fib  *instance
	taps [2]*instance
}

// newFibBed starts a bed of the devices port0, eth0 and eth1 with the test FIB
// on port0.
func newFibBed(t *testing.T) *fibBed {
	t.Helper()

	bed := newBed(t, func(config *dataplaneut.Config) {
		config.Devices = []string{"port0", "eth0", "eth1"}
	})
	config, err := buildFib(bed.agent, fibInput())
	require.NoError(t, err)
	bed.freeAtEnd(config)
	fib := &instance{moduleType: "fib", name: "fib", config: config}
	taps := [2]*instance{bed.tapInstance("tap-eth0"), bed.tapInstance("tap-eth1")}
	bed.publish(
		route{device: "port0", input: []*instance{fib}},
		route{device: "eth0", output: []*instance{taps[0]}},
		route{device: "eth1", output: []*instance{taps[1]}},
	)
	return &fibBed{bed: bed, fib: fib, taps: taps}
}

// counts reads what the bed counts on worker 0.
func (m *fibBed) counts() fibCounts {
	return fibCounts{
		outputs: [2]uint64{
			m.counter(m.taps[0], "rx")[0][0],
			m.counter(m.taps[1], "rx")[0][0],
		},
		routes: [3]uint64{
			m.counter(m.fib, "via_eth0")[0][0],
			m.counter(m.fib, "via_eth0_backup")[0][0],
			m.counter(m.fib, "via_eth1")[0][0],
		},
	}
}

// send runs the frame through the FIB on worker 0, and returns the result and
// what the frame added to the counts.
func (m *fibBed) send(frame []byte) (*dataplaneut.RawResult, fibCounts) {
	m.t.Helper()

	before := m.counts()
	result := m.run(0, frame)
	after := m.counts()
	var delta fibCounts
	for idx := range delta.outputs {
		delta.outputs[idx] = after.outputs[idx] - before.outputs[idx]
	}
	for idx := range delta.routes {
		delta.routes[idx] = after.routes[idx] - before.routes[idx]
	}
	return result, delta
}

// fibProbes are frames to destinations of the test FIB and where the FIB sends
// each: the output of eth0 or eth1, the routes it counts and the addresses it
// writes, or nowhere.
var fibProbes = []struct {
	name      string
	frame     []byte
	outputs   [2]uint64
	routes    [3]uint64
	addresses []byte
}{
	{
		name:      "IPv4 in the broad prefix",
		frame:     ipv4Frame("10.9.9.9"),
		outputs:   [2]uint64{1, 0},
		routes:    [3]uint64{1, 1, 0},
		addresses: []byte{0, 0, 0, 0, 0, 1, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:      "IPv4 in the narrower prefix",
		frame:     ipv4Frame("10.1.9.9"),
		outputs:   [2]uint64{0, 1},
		routes:    [3]uint64{0, 0, 1},
		addresses: []byte{0, 0, 0, 0, 0, 2, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:      "IPv4 in the narrowest prefix",
		frame:     ipv4Frame("10.1.2.3"),
		outputs:   [2]uint64{1, 0},
		routes:    [3]uint64{1, 1, 0},
		addresses: []byte{0, 0, 0, 0, 0, 1, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:    "IPv4 outside every prefix",
		frame:   ipv4Frame("11.0.0.1"),
		outputs: [2]uint64{0, 0},
		routes:  [3]uint64{0, 0, 0},
	},
	{
		name:      "IPv6 in a /64",
		frame:     ipv6Frame("2001:db8:3::1"),
		outputs:   [2]uint64{0, 1},
		routes:    [3]uint64{0, 0, 1},
		addresses: []byte{0, 0, 0, 0, 0, 2, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:      "IPv6 in the /32 beside the /64",
		frame:     ipv6Frame("2001:db8:3:1::1"),
		outputs:   [2]uint64{1, 0},
		routes:    [3]uint64{1, 1, 0},
		addresses: []byte{0, 0, 0, 0, 0, 1, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:      "IPv6 in the /32",
		frame:     ipv6Frame("2001:db8:ffff::1"),
		outputs:   [2]uint64{1, 0},
		routes:    [3]uint64{1, 1, 0},
		addresses: []byte{0, 0, 0, 0, 0, 1, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee},
	},
	{
		name:    "IPv6 outside every prefix",
		frame:   ipv6Frame("2002::1"),
		outputs: [2]uint64{0, 0},
		routes:  [3]uint64{0, 0, 0},
	},
}

// Test_Fib_Destination_SelectsTheDeviceOutput verifies that the longest
// prefix that holds the destination of a frame selects the route list, and the
// frame leaves through the output of the device of the list's first route; a
// destination in no prefix reaches no output.
func Test_Fib_Destination_SelectsTheDeviceOutput(t *testing.T) {
	bed := newFibBed(t)

	for _, tc := range fibProbes {
		t.Run(tc.name, func(t *testing.T) {
			_, delta := bed.send(tc.frame)

			require.Equal(t, tc.outputs, delta.outputs)
		})
	}
}

// Test_Fib_Destination_CountsEveryRouteOfTheList verifies that a frame is
// counted on every route of the list its destination selects, and on no other.
func Test_Fib_Destination_CountsEveryRouteOfTheList(t *testing.T) {
	bed := newFibBed(t)

	for _, tc := range fibProbes {
		t.Run(tc.name, func(t *testing.T) {
			_, delta := bed.send(tc.frame)

			require.Equal(t, tc.routes, delta.routes)
		})
	}
}

// Test_Fib_Destination_AddressesTheFrameFromTheFirstRoute verifies that a
// frame leaves with the neighbour of the first route of its list as
// destination and the source the route carries.
func Test_Fib_Destination_AddressesTheFrameFromTheFirstRoute(t *testing.T) {
	bed := newFibBed(t)

	for _, tc := range fibProbes {
		if tc.addresses == nil {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			result, _ := bed.send(tc.frame)

			require.Len(t, result.Output, 1)
			require.Equal(t, tc.addresses, result.Output[0][:12])
		})
	}
}

// Test_Fib_UnroutedDestination_IsDropped verifies that a frame whose
// destination is in no prefix is dropped as it came.
func Test_Fib_UnroutedDestination_IsDropped(t *testing.T) {
	bed := newFibBed(t)

	for _, tc := range fibProbes {
		if tc.addresses != nil {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			result, _ := bed.send(tc.frame)

			require.Equal(t, [][]byte{tc.frame}, result.Drop)
		})
	}
}

// Test_Fib_IPv6Subnets_BeyondTheFirstPageChunk_RouteToTheirList verifies that
// every one of twelve /64 prefixes, enough for the table to grow past its first
// chunk of pages, selects its own list under the /32 that holds them.
func Test_Fib_IPv6Subnets_BeyondTheFirstPageChunk_RouteToTheirList(t *testing.T) {
	bed := newFibBed(t)

	for subnet := 1; subnet <= 12; subnet++ {
		t.Run(fmt.Sprintf("subnet %d", subnet), func(t *testing.T) {
			_, delta := bed.send(ipv6Frame(fmt.Sprintf("2001:db8:%x::1", subnet)))

			require.Equal(t, [2]uint64{0, 1}, delta.outputs)
		})
	}
}

// reversedFibInput returns the test FIB with an IPv4 range that ends before
// it starts.
func reversedFibInput() sdkfixture.Fib {
	input := fibInput()
	input.V4 = append(input.V4, sdkfixture.Range4{From: [4]byte{10, 1, 2, 3}, To: [4]byte{10, 1, 2, 2}})
	return input
}

// Test_Fib_Build_ReversedRange_IsInvalidArgument verifies that a build refuses
// an LPM range that ends before it starts.
func Test_Fib_Build_ReversedRange_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)

	_, err := buildFib(bed.agent, reversedFibInput())

	require.ErrorIs(t, err, sdkfixture.ErrInvalidArgument)
}

// Test_Fib_Build_ReversedRange_ReturnsMemoryToBaseline verifies that a build
// that refuses a reversed range gives back what it built before the table.
func Test_Fib_Build_ReversedRange_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)

	_, err := buildFib(bed.agent, reversedFibInput())

	require.Error(t, err)
	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

// Test_Fib_Build_RouteOfAnAbsentDevice_IsInvalidArgument verifies that a
// build refuses a route naming a device past the list, before it allocates
// anything.
func Test_Fib_Build_RouteOfAnAbsentDevice_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	input := fibInput()
	input.Routes[1].Device = len(input.Devices)

	_, err := buildFib(bed.agent, input)

	require.ErrorIs(t, err, sdkfixture.ErrInvalidArgument)
	require.Equal(t, bed.baseline, freeSize(bed.agent))
}

// refusedNames are the names that do not fit a name field whole.
func refusedNames(field int) []struct {
	name string
	text string
} {
	return []struct {
		name string
		text string
	}{
		{name: "name with a NUL", text: "eth\x000"},
		{name: "name without room for the terminator", text: strings.Repeat("n", field)},
	}
}

// Test_Fib_Build_RefusedDeviceName_IsInvalidName verifies that a device name
// that holds a NUL or leaves no room for the terminator is refused instead of
// being cut short.
func Test_Fib_Build_RefusedDeviceName_IsInvalidName(t *testing.T) {
	for _, tc := range refusedNames(len(layout.CpModuleDevice{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			input := fibInput()
			input.Devices[0] = tc.text

			_, err := buildFib(bed.agent, input)

			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_Fib_Build_RefusedCounterName_IsInvalidName verifies that a counter
// name that holds a NUL or leaves no room for the terminator is refused
// instead of being cut short.
func Test_Fib_Build_RefusedCounterName_IsInvalidName(t *testing.T) {
	for _, tc := range refusedNames(len(layout.Counter{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			input := fibInput()
			input.Routes[0].Counter = tc.text

			_, err := buildFib(bed.agent, input)

			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_Fib_Build_LongestDeviceName_IsAccepted verifies that a device name
// which leaves room for the terminator fits whole.
func Test_Fib_Build_LongestDeviceName_IsAccepted(t *testing.T) {
	bed := newBed(t)
	input := fibInput()
	input.Devices[0] = strings.Repeat("n", len(layout.CpModuleDevice{}.Name)-1)

	config, err := buildFib(bed.agent, input)

	require.NoError(t, err)
	require.NoError(t, config.Free())
}

// Test_Fib_Build_NameWithNUL_IsInvalidName verifies that a configuration name
// that holds a NUL is refused instead of being cut short at it.
func Test_Fib_Build_NameWithNUL_IsInvalidName(t *testing.T) {
	bed := newBed(t)

	_, err := sdkfixture.NewFibConfig(bed.agent, "con\x00fig", sdkfixture.Fib{})

	require.ErrorIs(t, err, cpconfig.ErrInvalidName)
}

// sweep builds a configuration over a fresh agent of each memory limit in
// turn, from start up to end in steps, until a build fits, and returns the
// limit that fits, zero when none does.
//
// Each build that fails must be refused as exhausted memory and leave the
// agent's free memory as it was; the one that fits is freed, which must
// restore it as well. The agents live as long as the harness does.
func sweep(
	t *testing.T,
	bed *bed,
	build func(owner *agent.Agent) (*moduleConfig, error),
	start, end, stride int,
) int {
	t.Helper()

	for step, limit := 0, start; limit < end; step, limit = step+1, limit+stride {
		name := fmt.Sprintf("sweep-%d-%d", limit, step)
		owner, err := agent.Attach(bed.instance, name, uint64(limit))
		if err != nil {
			require.Zero(t, limit, "the agent attaches at any limit but zero")
			continue
		}
		baseline := freeSize(owner)

		config, err := build(owner)
		if err == nil {
			require.NoError(t, config.Free())
		} else {
			require.ErrorIs(t, err, shmalloc.ErrNoMemory, "a limit of %d bytes", limit)
		}

		require.Equal(t, baseline, freeSize(owner), "a limit of %d bytes", limit)
		if err == nil {
			return limit
		}
	}
	return 0
}

// buildFib builds a FIB named fib of the input over the agent, without
// registering it for cleanup.
func buildFib(owner *agent.Agent, input sdkfixture.Fib) (*moduleConfig, error) {
	module, err := sdkfixture.NewFibConfig(owner, "fib", input)
	return moduleHandle(module, err, sdkfixture.FreeFibConfig)
}

// buildTestFib builds the test FIB over the agent for a sweep.
func buildTestFib(owner *agent.Agent) (*moduleConfig, error) {
	return buildFib(owner, fibInput())
}

// Test_Fib_Build_NoRoomForBlocksOrHeader_ReturnsMemoryToBaseline verifies that
// a build which meets the end of the agent's memory in a block or in the
// header refuses and gives back all it took, at every limit far below the
// first table.
func Test_Fib_Build_NoRoomForBlocksOrHeader_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(64 * datasize.MB)
	})

	fits := sweep(t, bed, buildTestFib, 0, 16<<10, 64)

	require.Zero(t, fits)
}

// Test_Fib_Build_CMemoryRunsOut_ReturnsMemoryToBaseline verifies that a build
// whose prefix tables run out of memory gives back all it took, at every
// capacity.
//
// A buddy allocator fails monotonically with its capacity, so a sweep up in
// steps well below a page chunk crosses each failure point in turn: the first
// table, its growth, and the second table after the first was written.
func Test_Fib_Build_CMemoryRunsOut_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	start := 16 << 10

	fits := sweep(t, bed, buildTestFib, start, 8<<20, 8<<10)

	require.Greater(t, fits, start, "a build fits only above where the first tables fail")
}

// Test_Fib_Build_EachAllocationRefused_ReturnsMemoryToBaseline verifies that
// a build refused at any of its allocations, those of its prefix tables
// included, refuses as exhausted and gives back all it took.
func Test_Fib_Build_EachAllocationRefused_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)

	sweepAllocations(t, bed.agent, func(owner *agent.Agent) (func() error, error) {
		config, err := buildTestFib(owner)
		if err != nil {
			return nil, err
		}
		return config.Free, nil
	})
}
