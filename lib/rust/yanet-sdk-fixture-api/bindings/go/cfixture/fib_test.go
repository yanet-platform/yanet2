//go:build sdk_rust_fixtures

package cfixture_test

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

func prefix4(prefix string, value uint32) cfixture.Range4 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	return cfixture.NewRange4(parsed.Masked().Addr().As4(), network.LastAddr().As4(), value)
}

func prefix6(prefix string, value uint32) cfixture.Range6 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	return cfixture.NewRange6(parsed.Masked().Addr().As16(), network.LastAddr().As16(), value)
}

// fibInput exercises three routes, two devices and overlapping IP prefixes.
//
// The two route lists select routes zero and two, then route one. Broad ranges
// precede narrower overrides. Twelve IPv6 /64 prefixes under a /32 force table
// growth beyond the initial page chunk. The default declaration separates the
// two named devices.
func fibInput(t *testing.T) cfixture.Fib {
	t.Helper()

	devices := make([]cfixture.FibDevice, 0, 3)
	for _, name := range []string{"eth0", "", "eth1"} {
		device, err := cfixture.NewFibDevice(name)
		require.NoError(t, err)
		devices = append(devices, device)
	}
	routes := make([]cfixture.FibRoute, 0, 3)
	for _, spec := range []struct {
		neighbour byte
		device    uint16
		counter   string
	}{
		{neighbour: 1, device: 0, counter: "via_eth0"},
		{neighbour: 2, device: 2, counter: "via_eth1"},
		{neighbour: 3, device: 0, counter: "via_eth0_backup"},
	} {
		route, err := cfixture.NewFibRoute([6]byte{0, 0, 0, 0, 0, spec.neighbour}, spec.device, spec.counter)
		require.NoError(t, err)
		routes = append(routes, route)
	}
	v6 := []cfixture.Range6{prefix6("2001:db8::/32", 0)}
	for subnet := 1; subnet <= 12; subnet++ {
		v6 = append(v6, prefix6(fmt.Sprintf("2001:db8:%x::/64", subnet), 1))
	}
	return cfixture.Fib{
		Devices: devices,
		Routes:  routes,
		Lists:   []cfixture.FibList{cfixture.NewFibList(0, 2), cfixture.NewFibList(2, 1)},
		Indexes: []uint64{0, 2, 1},
		V6:      v6,
		V4: []cfixture.Range4{
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

// fibCounts combines device-output counts with the three registered route
// counters.
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
	config, err := cfixture.NewFibConfig(bed.agent, "fib", fibInput(t))
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

// fibProbes covers matched and unmatched destinations and their expected
// forwarding effects.
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

// Test_Fib_Destination_SelectsTheDeviceOutput verifies that longest-prefix
// selection chooses the device output; misses reach no output.
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

// Test_Fib_Destination_AddressesTheFrameFromTheFirstRoute verifies that the
// first route supplies both frame addresses.
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
// all subnets survive table growth.
func Test_Fib_IPv6Subnets_BeyondTheFirstPageChunk_RouteToTheirList(t *testing.T) {
	bed := newFibBed(t)

	for subnet := 1; subnet <= 12; subnet++ {
		t.Run(fmt.Sprintf("subnet %d", subnet), func(t *testing.T) {
			_, delta := bed.send(ipv6Frame(fmt.Sprintf("2001:db8:%x::1", subnet)))

			require.Equal(t, [2]uint64{0, 1}, delta.outputs)
		})
	}
}

// Test_Fib_Build_ReversedRange_IsInvalidArgument verifies that a build refuses
// an LPM range that ends before it starts, and says why.
func Test_Fib_Build_ReversedRange_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)
	input := fibInput(t)
	input.V4 = append(input.V4, cfixture.NewRange4([4]byte{10, 1, 2, 3}, [4]byte{10, 1, 2, 2}, 0))

	_, err := cfixture.NewFibConfig(bed.agent, "fib", input)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
	require.ErrorContains(t, err, "an LPM range ends before it starts")
}

// Test_Fib_Build_ReversedRange_ReturnsMemoryToBaseline verifies that a build
// that refuses a reversed range gives back what it built before the table.
func Test_Fib_Build_ReversedRange_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	input := fibInput(t)
	input.V4 = append(input.V4, cfixture.NewRange4([4]byte{10, 1, 2, 3}, [4]byte{10, 1, 2, 2}, 0))

	_, err := cfixture.NewFibConfig(bed.agent, "fib", input)

	require.Error(t, err)
	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// sweep finds the first memory limit that admits a complete FIB build.
//
// Each fresh agent survives until harness cleanup. Every failed build must
// report exhausted memory or an untagged registry refusal and restore its
// allocation baseline. A successful build must also restore that baseline after
// release.
func sweep(t *testing.T, bed *bed, input cfixture.Fib, start, end, stride int) int {
	t.Helper()

	for step, limit := 0, start; limit < end; step, limit = step+1, limit+stride {
		name := fmt.Sprintf("sweep-%d-%d", limit, step)
		agent, err := bed.harness.SharedMemory().AgentAttach(name, 0, datasize.ByteSize(limit))
		if err != nil {
			require.Zero(t, limit, "the agent attaches at any limit but zero")
			continue
		}
		baseline := agent.BlockAllocatorFreeSize()

		config, err := cfixture.NewFibConfig(agent, "fib", input)
		if err == nil {
			require.NoError(t, config.Free())
		} else if !exhaustedOrUntagged(err) {
			require.FailNow(t, "unexpected refusal", "%d bytes: %v", limit, err)
		}

		require.Equal(t, baseline, agent.BlockAllocatorFreeSize(), "a limit of %d bytes", limit)
		if err == nil {
			return limit
		}
	}
	return 0
}

// exhaustedOrUntagged reports whether the error is a refusal for lack of
// memory or carries no kind.
func exhaustedOrUntagged(err error) bool {
	for _, kind := range []error{
		ffi.ErrNotFound,
		ffi.ErrFailedPrecondition,
		ffi.ErrBusy,
		ffi.ErrInvalidArgument,
	} {
		if errors.Is(err, kind) {
			return false
		}
	}
	return true
}

// Test_Fib_Build_NoRoomForBlocksOrHeader_ReturnsMemoryToBaseline verifies that
// exhaustion restores every allocation.
func Test_Fib_Build_NoRoomForBlocksOrHeader_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(64 * datasize.MB)
	})

	fits := sweep(t, bed, fibInput(t), 0, 16<<10, 64)

	require.Zero(t, fits)
}

// Test_Fib_Build_CMemoryRunsOut_ReturnsMemoryToBaseline verifies that a build
// whose C memory runs out gives back all it took, at every capacity.
//
// A buddy allocator fails monotonically with its capacity, so a sweep up in
// steps well below a page chunk crosses each failure point in turn: the first
// table, its growth, and the second table after the first was written.
func Test_Fib_Build_CMemoryRunsOut_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	start := 16 << 10

	fits := sweep(t, bed, fibInput(t), start, 8<<20, 8<<10)

	require.Greater(t, fits, start, "a build fits only above where the first tables fail")
}

// Test_Fib_DeviceDeclarations_ValidateEveryNameAndLogicalIndex verifies that
// invalid declarations and route indices restore all configuration memory.
func Test_Fib_DeviceDeclarations_ValidateEveryNameAndLogicalIndex(t *testing.T) {
	bed := newBed(t)
	for _, tc := range []struct {
		name        string
		names       []string
		routeDevice *uint16
		message     string
	}{
		{name: "duplicate nonempty name", names: []string{"eth0", "eth0"}, message: "device declarations are repeated or noncontiguous"},
		{name: "duplicate default first", names: []string{"", ""}, message: "device declarations are repeated or noncontiguous"},
		{name: "duplicate default after named", names: []string{"eth0", "", ""}, message: "device declarations are repeated or noncontiguous"},
		{name: "invalid unused name", names: []string{"eth0", "\xff"}, message: "a name is not NUL terminated UTF-8"},
		{name: "route past declaration table", names: []string{"eth0"}, routeDevice: new(uint16(1)), message: "a route names a device out of the list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := cfixture.Fib{}
			for _, name := range tc.names {
				device, err := cfixture.NewFibDevice(name)
				require.NoError(t, err)
				input.Devices = append(input.Devices, device)
			}
			if tc.routeDevice != nil {
				route, err := cfixture.NewFibRoute([6]byte{}, *tc.routeDevice, "route")
				require.NoError(t, err)
				input.Routes = []cfixture.FibRoute{route}
			}
			_, err := cfixture.NewFibConfig(bed.agent, "fib", input)
			require.ErrorIs(t, err, ffi.ErrInvalidArgument)
			require.ErrorContains(t, err, tc.message)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_Fib_DeviceDeclarations_DefaultAtEitherEndAndEmptyTableRelease verifies
// that supported declaration layouts build and return their native resources.
func Test_Fib_DeviceDeclarations_DefaultAtEitherEndAndEmptyTableRelease(t *testing.T) {
	bed := newBed(t)
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{name: "default first", names: []string{"", "eth0", "eth1"}},
		{name: "default last", names: []string{"eth0", "eth1", ""}},
		{name: "no declarations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := cfixture.Fib{}
			for _, name := range tc.names {
				device, err := cfixture.NewFibDevice(name)
				require.NoError(t, err)
				input.Devices = append(input.Devices, device)
			}
			config, err := cfixture.NewFibConfig(bed.agent, "fib", input)
			require.NoError(t, err)
			require.NoError(t, config.Free())
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}
