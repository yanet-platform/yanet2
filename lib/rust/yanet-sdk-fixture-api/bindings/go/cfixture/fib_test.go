//go:build sdk_rust_fixtures

package cfixture_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
)

// prefix4 returns the range of the IPv4 prefix, mapped to the value.
func prefix4(prefix string, value uint32) cfixture.Range4 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	first := parsed.Masked().Addr().As4()
	return cfixture.NewRange4(first, network.LastAddr().As4(), value)
}

// prefix6 returns the range of the IPv6 prefix, mapped to the value.
func prefix6(prefix string, value uint32) cfixture.Range6 {
	parsed := netip.MustParsePrefix(prefix)
	network, _ := xnetip.NetworkFromPrefix(parsed)
	first := parsed.Masked().Addr().As16()
	return cfixture.NewRange6(first, network.LastAddr().As16(), value)
}

// fibInput returns a FIB of three routes over two devices, eth0 and eth1, with
// two route lists, routes 0 and 2 and then route 1, overlapping IPv4 prefixes
// and twelve IPv6 /64 prefixes under a /32.
//
// The ranges are in insertion order, the broad before the narrow, as the later
// insert wins on an overlap. The twelve /64 prefixes are enough for the IPv6
// table to need more pages than its first chunk holds.
func fibInput(t *testing.T) cfixture.Fib {
	t.Helper()

	devices := make([]cfixture.FibDevice, 0, 2)
	for _, name := range []string{"eth0", "eth1"} {
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
		{neighbour: 2, device: 1, counter: "via_eth1"},
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

// sweep builds a configuration in a fresh agent of each memory limit in turn,
// from start up to end in steps, until a build fits, and returns the limit
// that fits, zero when none does.
//
// Each build that fails must be refused as exhausted memory, or by the header's
// registries, which C reports without a kind, and leave the agent's free memory
// as it was; the one that fits is freed, which must restore it as well. The
// agents live as long as the harness does.
func sweep(
	t *testing.T,
	bed *bed,
	build func(*ffi.Agent) (*cfixture.ModuleConfig, error),
	start, end, stride int,
) int {
	t.Helper()

	for step, limit := 0, start; limit < end; step, limit = step+1, limit+stride {
		name := fmt.Sprintf("sweep-%d-%d", limit, step)
		agent, err := bed.harness.SharedMemory().AgentAttach(name, 0, datasize.ByteSize(limit))
		if err != nil {
			require.Zero(t, limit, "the agent attaches at any limit but zero")
			continue
		}
		baseline := agent.BlockAllocatorFreeSize()

		config, err := build(agent)
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

// buildFib returns the build of the FIB of the input, named fib, for a sweep.
func buildFib(input cfixture.Fib) func(*ffi.Agent) (*cfixture.ModuleConfig, error) {
	return func(agent *ffi.Agent) (*cfixture.ModuleConfig, error) {
		return cfixture.NewFibConfig(agent, "fib", input)
	}
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
// a build which meets the end of the agent's memory in a block or in the
// header refuses and gives back all it took, at every limit far below the
// first table.
func Test_Fib_Build_NoRoomForBlocksOrHeader_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(64 * datasize.MB)
	})

	fits := sweep(t, bed, buildFib(fibInput(t)), 0, 16<<10, 64)

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

	fits := sweep(t, bed, buildFib(fibInput(t)), start, 8<<20, 8<<10)

	require.Greater(t, fits, start, "a build fits only above where the first tables fail")
}

// fibRoute is a neighbour of the benchmark FIB: its address, its device and
// the counter it counts in.
type fibRoute struct {
	neighbour byte
	device    uint16
	counter   string
}

// fibBenchDevices are the devices of the benchmark FIB.
var fibBenchDevices = []string{"eth0", "eth1", "eth2", "eth3"}

// fibBenchRoutes are eight neighbours, two per device.
var fibBenchRoutes = func() []fibRoute {
	routes := make([]fibRoute, 0, 8)
	for idx := range 8 {
		routes = append(routes, fibRoute{
			neighbour: byte(idx + 1),
			device:    uint16(idx % len(fibBenchDevices)),
			counter:   fmt.Sprintf("nexthop_%d", idx),
		})
	}
	return routes
}()

// fibBenchLists are the next-hop lists of the benchmark FIB, single routes
// and ECMP groups of two and four.
var fibBenchLists = [][]uint32{{0}, {1}, {2, 3}, {4, 5}, {0, 2, 4, 6}, {1, 3, 5, 7}}

// fibPrefix is a prefix of the benchmark FIB and the list it routes to.
type fibPrefix struct {
	prefix netip.Prefix
	list   uint32
}

// fibBenchPrefixes returns count prefixes, three in four IPv6, with the
// prefix lengths of a full table: mostly /24 and /22../23 for IPv4 and /48,
// /32../47 and /56../64 for IPv6, each to a random list; deterministic from
// the seed and ordered by length, the broad before the narrow, as the later
// insert wins on an overlap.
func fibBenchPrefixes(seed uint64, count int) []fibPrefix {
	random := rand.New(rand.NewPCG(seed, 0xf1b))
	prefixes := make([]fibPrefix, 0, count)
	for idx := range count {
		var prefix netip.Prefix
		if idx%4 == 0 {
			var bits int
			switch pick := random.IntN(100); {
			case pick < 60:
				bits = 24
			case pick < 80:
				bits = 22 + random.IntN(2)
			case pick < 92:
				bits = 16 + random.IntN(6)
			case pick < 97:
				bits = 25 + random.IntN(8)
			default:
				bits = 8 + random.IntN(8)
			}
			addr := [4]byte{byte(1 + random.IntN(223)), byte(random.IntN(256)), byte(random.IntN(256)), byte(random.IntN(256))}
			prefix = netip.PrefixFrom(netip.AddrFrom4(addr), bits).Masked()
		} else {
			var bits int
			switch pick := random.IntN(100); {
			case pick < 45:
				bits = 48
			case pick < 70:
				bits = 32 + random.IntN(16)
			case pick < 95:
				bits = 56 + random.IntN(9)
			default:
				bits = 19 + random.IntN(13)
			}
			addr := [16]byte{0x20 | byte(random.IntN(16)), byte(random.IntN(256))}
			for octet := 2; octet < 8; octet++ {
				addr[octet] = byte(random.IntN(256))
			}
			prefix = netip.PrefixFrom(netip.AddrFrom16(addr), bits).Masked()
		}
		prefixes = append(prefixes, fibPrefix{prefix: prefix, list: uint32(random.IntN(len(fibBenchLists)))})
	}
	slices.SortStableFunc(prefixes, func(left, right fibPrefix) int {
		return left.prefix.Bits() - right.prefix.Bits()
	})
	return prefixes
}

// lastAddr returns the last address of the prefix.
func lastAddr(prefix netip.Prefix) netip.Addr {
	network, _ := xnetip.NetworkFromPrefix(prefix)
	return network.LastAddr()
}

// sdkFibInput returns the SDK FIB of the benchmark devices, routes and lists
// over the prefixes.
func sdkFibInput(b *testing.B, prefixes []fibPrefix) cfixture.Fib {
	b.Helper()

	var input cfixture.Fib
	for _, name := range fibBenchDevices {
		device, err := cfixture.NewFibDevice(name)
		require.NoError(b, err)
		input.Devices = append(input.Devices, device)
	}
	for _, route := range fibBenchRoutes {
		record, err := cfixture.NewFibRoute([6]byte{0, 0, 0, 0, 0, route.neighbour}, route.device, route.counter)
		require.NoError(b, err)
		input.Routes = append(input.Routes, record)
	}
	for _, list := range fibBenchLists {
		input.Lists = append(input.Lists, cfixture.NewFibList(uint64(len(input.Indexes)), uint64(len(list))))
		for _, route := range list {
			input.Indexes = append(input.Indexes, uint64(route))
		}
	}
	for _, item := range prefixes {
		if item.prefix.Addr().Is4() {
			input.V4 = append(input.V4, prefix4(item.prefix.String(), item.list))
		} else {
			input.V6 = append(input.V6, prefix6(item.prefix.String(), item.list))
		}
	}
	return input
}

// fibRange is a prefix of the benchmark FIB as the C object takes it: its
// first and last addresses, and its list.
type fibRange struct {
	from, to netip.Addr
	list     uint32
}

// cFibRanges returns the ranges of the prefixes.
func cFibRanges(prefixes []fibPrefix) []fibRange {
	ranges := make([]fibRange, 0, len(prefixes))
	for _, item := range prefixes {
		ranges = append(ranges, fibRange{from: item.prefix.Addr(), to: lastAddr(item.prefix), list: item.list})
	}
	return ranges
}

// buildCFib builds the C route FIB object of the benchmark devices, routes
// and lists over the ranges, the way the route module builds one.
//
// The C API takes one call per route, list and range, so those calls are part
// of what a C build costs.
func buildCFib(agent *ffi.Agent, ranges []fibRange) (*croute.FIBObject, error) {
	object, err := croute.NewFIBObject(agent, "fib")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*croute.FIBObject, error) {
		_ = object.Free()
		return nil, err
	}
	src := net.HardwareAddr{0xee, 0xee, 0xee, 0xee, 0xee, 0xee}
	for _, route := range fibBenchRoutes {
		dst := net.HardwareAddr{0, 0, 0, 0, 0, route.neighbour}
		if _, err := object.AddRoute(src, dst, uint32(route.device), route.counter); err != nil {
			return fail(err)
		}
	}
	for _, list := range fibBenchLists {
		if _, err := object.AddRouteList(list); err != nil {
			return fail(err)
		}
	}
	for _, item := range ranges {
		if err := object.AddRange(item.from, item.to, item.list); err != nil {
			return fail(err)
		}
	}
	return object, nil
}

// BenchmarkFibBuild measures the build of a FIB of 100k prefixes, three in
// four IPv6, over four devices and six next-hop lists: the SDK FIB through the
// api, and the C route FIB object the route module builds, from the same
// input.
func BenchmarkFibBuild(b *testing.B) {
	prefixes := fibBenchPrefixes(1, 100_000)
	bed := newBed(b, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(2 * datasize.GB)
		config.WorkerCount = 1
		config.ObjectsToLoad = []string{"route_fib"}
	}).withAgent("fib-bench", 1536*datasize.MB)

	// Both sides compute their input records before the timer runs.
	b.Run("impl=c", func(b *testing.B) {
		bed := bed.on(b)
		ranges := cFibRanges(prefixes)
		bed.benchmarkBuild(b, func() (buildable, error) {
			return buildCFib(bed.agent, ranges)
		}, nil)
	})
	b.Run("impl=sdk", func(b *testing.B) {
		bed := bed.on(b)
		input := sdkFibInput(b, prefixes)
		bed.benchmarkBuild(b, func() (buildable, error) {
			return cfixture.NewFibConfig(bed.agent, "fib", input)
		}, nil)
	})
}
