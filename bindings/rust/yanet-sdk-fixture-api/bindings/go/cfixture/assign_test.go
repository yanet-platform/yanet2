//go:build sdk_rust_fixtures

package cfixture_test

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

const (
	protoTCP = 6
	protoUDP = 17
)

// assignRule is a rule of an assign test: its networks, protocols and ports,
// each empty for any, and what it does.
//
// A network is a prefix, or a C record the prefix cannot spell, which only
// the cgo build takes.
type assignRule struct {
	family   uint64
	src, dst []any
	protos   []classify.LineRange
	ports    []classify.PortRange
	pass     bool
	counter  string
}

// assignOf lays the rules out over the arrays of an assign build.
func assignOf(t *testing.T, rules ...assignRule) cfixture.Assign {
	t.Helper()

	var assign cfixture.Assign
	nets := func(family uint64, items []any) cfixture.Span {
		if family == cfixture.AssignFamilyIPv4 {
			offset := len(assign.Nets4)
			for _, item := range items {
				record, ok := item.(cfixture.Net4)
				if prefix, isPrefix := item.(netip.Prefix); isPrefix {
					record, ok = cfixture.NewNet4(prefix.Addr().As4(), [4]byte(net.CIDRMask(prefix.Bits(), 32))), true
				}
				require.True(t, ok, "IPv4 network %v", item)
				assign.Nets4 = append(assign.Nets4, record)
			}
			return cfixture.NewSpan(uint64(offset), uint64(len(items)))
		}
		offset := len(assign.Nets6)
		for _, item := range items {
			record, ok := item.(cfixture.Net6)
			if prefix, isPrefix := item.(netip.Prefix); isPrefix {
				record, ok = cfixture.NewNet6(prefix.Addr().As16(), [16]byte(net.CIDRMask(prefix.Bits(), 128))), true
			}
			require.True(t, ok, "IPv6 network %v", item)
			assign.Nets6 = append(assign.Nets6, record)
		}
		return cfixture.NewSpan(uint64(offset), uint64(len(items)))
	}
	for _, rule := range rules {
		src, dst := nets(rule.family, rule.src), nets(rule.family, rule.dst)
		protos := cfixture.NewSpan(uint64(len(assign.Lines)), uint64(len(rule.protos)))
		for _, lines := range rule.protos {
			assign.Lines = append(assign.Lines, cfixture.NewLineRange(lines.From, lines.To))
		}
		ports := cfixture.NewSpan(uint64(len(assign.Ports)), uint64(len(rule.ports)))
		for _, port := range rule.ports {
			assign.Ports = append(assign.Ports, cfixture.NewPortRange(port.From, port.To))
		}
		record, err := cfixture.NewAssignRule(rule.family, src, dst, protos, ports, rule.pass, rule.counter)
		require.NoError(t, err)
		assign.Rules = append(assign.Rules, record)
	}
	return assign
}

// assignBuild builds an assign configuration named config of the rules over
// an agent of the bed's harness.
type assignBuild func(t *testing.T, bed *bed, agent *ffi.Agent, rules []assignRule) (moduleConfig, error)

// buildAssignCgo builds through the SDK's Rust builder of the fixture api.
func buildAssignCgo(t *testing.T, _ *bed, agent *ffi.Agent, rules []assignRule) (moduleConfig, error) {
	return cfixture.NewAssignConfig(agent, "config", assignOf(t, rules...))
}

// buildAssignNative builds through the native Go builder over the agent's
// native view; the rules' networks must be prefixes.
func buildAssignNative(t *testing.T, bed *bed, agent *ffi.Agent, rules []assignRule) (moduleConfig, error) {
	families := map[uint64]sdkfixture.AssignFamily{
		cfixture.AssignFamilyAny:  sdkfixture.AssignAny,
		cfixture.AssignFamilyIPv4: sdkfixture.AssignIPv4,
		cfixture.AssignFamilyIPv6: sdkfixture.AssignIPv6,
	}
	prefixes := func(items []any) []netip.Prefix {
		out := make([]netip.Prefix, 0, len(items))
		for _, item := range items {
			prefix, ok := item.(netip.Prefix)
			require.True(t, ok, "network %v is not a prefix", item)
			out = append(out, prefix)
		}
		return out
	}
	native := make([]sdkfixture.AssignRule, 0, len(rules))
	for _, rule := range rules {
		family, ok := families[rule.family]
		require.True(t, ok, "family %d", rule.family)
		native = append(native, sdkfixture.AssignRule{
			Family:  family,
			Src:     prefixes(rule.src),
			Dst:     prefixes(rule.dst),
			Protos:  rule.protos,
			Ports:   rule.ports,
			Pass:    rule.pass,
			Counter: rule.counter,
		})
	}
	module, err := sdkfixture.NewAssignConfig(bed.harness.NativeAgent(t, agent), "config", native)
	if err != nil {
		return nil, err
	}
	return nativeConfig{module: module}, nil
}

// nativeConfig is a configuration the native builder made, which the bed
// publishes through its cgo agent.
type nativeConfig struct {
	module *layout.CpModule
}

// AsFFIModule returns the handle the cgo agent publishes.
func (m nativeConfig) AsFFIModule() ffi.ModuleConfig {
	return ffi.NewModuleConfig(unsafe.Pointer(m.module))
}

// Free frees the configuration natively.
func (m nativeConfig) Free() error {
	return sdkfixture.FreeAssignConfig(m.module)
}

// forEachBuild runs an assign scenario once over each builder: the SDK's
// Rust builder through cgo and the native Go one.
func forEachBuild(t *testing.T, scenario func(t *testing.T, build assignBuild)) {
	for _, tc := range []struct {
		name  string
		build assignBuild
	}{
		{name: "cgo", build: buildAssignCgo},
		{name: "native", build: buildAssignNative},
	} {
		t.Run(tc.name, func(t *testing.T) { scenario(t, tc.build) })
	}
}

// assignInstance builds an assign configuration of the rules as an instance
// of the module type assign.
func (m *bed) assignInstance(build assignBuild, rules ...assignRule) *instance {
	m.t.Helper()

	config, err := build(m.t, m, m.agent, rules)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return &instance{moduleType: "assign", name: "config", config: config}
}

// net4 returns the IPv4 network of the address and prefix length.
func net4(addr [4]byte, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4(addr), bits)
}

// net6 returns the IPv6 network of the address and prefix length.
func net6(addr [16]byte, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom16(addr), bits)
}

// transportLen is the transport header the parser requires of the protocol.
func transportLen(proto byte) int {
	if proto == protoTCP {
		return 20
	}
	return 8
}

// ip4Frame returns an IPv4 frame of the protocol from src to dst whose
// transport header names the destination port, the fragment at the offset,
// in 8-byte units.
func ip4Frame(src, dst [4]byte, proto byte, dstPort, fragmentOffset uint16) []byte {
	payload := transportLen(proto)
	frame := make([]byte, 14+20+payload)
	frame[12], frame[13] = 0x08, 0x00
	header := frame[14:]
	header[0] = 0x45
	binary.BigEndian.PutUint16(header[2:], uint16(20+payload))
	binary.BigEndian.PutUint16(header[6:], fragmentOffset)
	header[8] = 64
	header[9] = proto
	copy(header[12:], src[:])
	copy(header[16:], dst[:])
	binary.BigEndian.PutUint16(header[20+2:], dstPort)
	return frame
}

// ip6Frame returns an IPv6 frame of the protocol from src to dst whose
// transport header names the destination port.
func ip6Frame(src, dst [16]byte, proto byte, dstPort uint16) []byte {
	payload := transportLen(proto)
	frame := make([]byte, 14+40+payload)
	frame[12], frame[13] = 0x86, 0xdd
	header := frame[14:]
	header[0] = 0x60
	binary.BigEndian.PutUint16(header[4:], uint16(payload))
	header[6] = proto
	header[7] = 64
	copy(header[8:], src[:])
	copy(header[24:], dst[:])
	binary.BigEndian.PutUint16(header[40+2:], dstPort)
	return frame
}

// addr6 returns the IPv6 address of the documentation prefix 2001:db8::/32
// with the last two bytes given.
func addr6(hi, lo byte) [16]byte {
	return [16]byte{0x20, 0x01, 0x0d, 0xb8, 14: hi, 15: lo}
}

// Test_Assign_FirstMatch_ByEachAttribute verifies that a packet takes the
// first rule it matches, the earlier of two that both match even when it is
// the wider, and that each attribute, through its joint, decides a match: the
// source and the destination networks, the protocol and the port.
func Test_Assign_FirstMatch_ByEachAttribute(t *testing.T) {
	forEachBuild(t, assignFirstMatchByEachAttribute)
}

// assignFirstMatchByEachAttribute runs the scenario of the test of
// its name over a build.
func assignFirstMatchByEachAttribute(t *testing.T, build assignBuild) {
	bed := newBed(t)
	inst := bed.assignInstance(build,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10}, 8)},
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}},
			counter: "dns",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10, 1}, 16)},
			dst:     []any{net4([4]byte{192, 0, 2}, 24)},
			pass:    true,
			counter: "inner",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10, 1}, 16)},
			pass:    false,
			counter: "elsewhere",
		},
	)
	bed.serveBeforeForward(inst)
	inner, outside := [4]byte{10, 1, 2, 3}, [4]byte{192, 0, 2, 1}
	frames := [][]byte{
		ip4Frame(inner, outside, protoUDP, 53, 0),
		ip4Frame(inner, outside, protoUDP, 80, 0),
		ip4Frame(inner, outside, protoTCP, 53, 0),
		ip4Frame(inner, [4]byte{198, 51, 100, 1}, protoUDP, 80, 0),
		ip4Frame([4]byte{10, 9, 9, 9}, outside, protoTCP, 53, 0),
	}

	result := bed.run(0, frames...)

	require.Equal(t,
		[]fate{fateDrop, fateOutput, fateOutput, fateDrop, fateDrop},
		fatesOf(result, frames),
	)
	udpLen, tcpLen := len(frames[0]), len(frames[2])
	for _, tc := range []struct {
		name    string
		counter string
		want    []uint64
	}{
		{name: "earlier wider rule by protocol and port", counter: "dns", want: packetsAndBytes(1, udpLen)},
		{name: "rule by both networks", counter: "inner", want: packetsAndBytes(2, udpLen+tcpLen)},
		{name: "rule by the source network only", counter: "elsewhere", want: packetsAndBytes(1, udpLen)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, bed.counter(inst, tc.counter)[0])
		})
	}
}

// Test_Assign_Families_ShareTheRuleIndex verifies that each family's packets
// are classified by the rules of their family alone, and that a rule's action
// is found by its index among the rules of both families.
func Test_Assign_Families_ShareTheRuleIndex(t *testing.T) {
	forEachBuild(t, assignFamiliesShareTheRuleIndex)
}

// assignFamiliesShareTheRuleIndex runs the scenario of the test of
// its name over a build.
func assignFamiliesShareTheRuleIndex(t *testing.T, build assignBuild) {
	bed := newBed(t)
	inst := bed.assignInstance(build,
		assignRule{
			family:  cfixture.AssignFamilyIPv6,
			src:     []any{net6(addr6(1, 0), 120)},
			ports:   []classify.PortRange{{From: 1000, To: 2000}},
			pass:    true,
			counter: "v6 ports",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			pass:    true,
			counter: "v4 any",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv6,
			counter: "v6 rest",
		},
	)
	bed.serveBeforeForward(inst)
	frames := [][]byte{
		ip6Frame(addr6(1, 7), addr6(9, 9), protoTCP, 1500),
		ip6Frame(addr6(1, 7), addr6(9, 9), protoTCP, 2001),
		ip6Frame(addr6(2, 7), addr6(9, 9), protoUDP, 1500),
		ip4Frame([4]byte{192, 0, 2, 1}, [4]byte{192, 0, 2, 2}, protoUDP, 1500, 0),
	}

	result := bed.run(0, frames...)

	require.Equal(t,
		[]fate{fateOutput, fateDrop, fateDrop, fateOutput},
		fatesOf(result, frames),
	)
	require.Equal(t, packetsAndBytes(1, len(frames[0])), bed.counter(inst, "v6 ports")[0])
	require.Equal(t, packetsAndBytes(2, len(frames[1])+len(frames[2])), bed.counter(inst, "v6 rest")[0])
	require.Equal(t, packetsAndBytes(1, len(frames[3])), bed.counter(inst, "v4 any")[0])
}

// Test_Assign_Unclassified_IsDropped verifies that a fragment after the first
// classifies as of port zero, and that a packet no rule matches, one of a
// family without rules and one that is not IP are dropped and counted by no
// rule.
func Test_Assign_Unclassified_IsDropped(t *testing.T) {
	forEachBuild(t, assignUnclassifiedIsDropped)
}

// assignUnclassifiedIsDropped runs the scenario of the test of
// its name over a build.
func assignUnclassifiedIsDropped(t *testing.T, build assignBuild) {
	bed := newBed(t)
	inst := bed.assignInstance(build,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			ports:   []classify.PortRange{{From: 53, To: 53}},
			pass:    true,
			counter: "dns",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			ports:   []classify.PortRange{{From: 0, To: 0}},
			counter: "port zero",
		},
	)
	bed.serveBeforeForward(inst)
	src, dst := [4]byte{192, 0, 2, 1}, [4]byte{192, 0, 2, 2}
	frames := [][]byte{
		ip4Frame(src, dst, protoUDP, 53, 0),
		ip4Frame(src, dst, protoUDP, 53, 185),
		ip4Frame(src, dst, protoUDP, 80, 0),
		ip6Frame(addr6(1, 1), addr6(1, 2), protoUDP, 53),
		arpFrames(1)[0],
	}

	result := bed.run(0, frames...)

	require.Equal(t,
		[]fate{fateOutput, fateDrop, fateDrop, fateDrop, fateDrop},
		fatesOf(result, frames),
	)
	require.Equal(t, packetsAndBytes(1, len(frames[0])), bed.counter(inst, "dns")[0])
	require.Equal(t, packetsAndBytes(1, len(frames[1])), bed.counter(inst, "port zero")[0])
}

// Test_Assign_Build_RefusesInvalidRules verifies that the build refuses a rule
// the C compile would read as other than it says, and a rule that names
// nothing it was given.
func Test_Assign_Build_RefusesInvalidRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		assign func(t *testing.T) cfixture.Assign
		want   string
	}{
		{
			name: "IPv4 mask that is not contiguous",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv4,
					src:    []any{cfixture.NewNet4([4]byte{10}, [4]byte{0xff, 0, 0xff})},
				})
			},
			want: "a network mask is not contiguous",
		},
		{
			name: "IPv6 mask that is not contiguous in its low half",
			assign: func(t *testing.T) cfixture.Assign {
				mask := [16]byte{0xff, 0xff, 8: 0x0f}
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv6,
					dst:    []any{cfixture.NewNet6(addr6(0, 0), mask)},
				})
			},
			want: "a network mask is not contiguous",
		},
		{
			name: "port range that ends before it starts",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv4,
					ports:  []classify.PortRange{{From: 80, To: 53}},
				})
			},
			want: "a range ends before it starts",
		},
		{
			name: "protocol range that ends before it starts",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv6,
					protos: []classify.LineRange{{From: 17, To: 6}},
				})
			},
			want: "a range ends before it starts",
		},
		{
			name: "run beyond its array",
			assign: func(t *testing.T) cfixture.Assign {
				assign := assignOf(t, assignRule{family: cfixture.AssignFamilyIPv4})
				rule, err := cfixture.NewAssignRule(
					cfixture.AssignFamilyIPv4,
					cfixture.NewSpan(0, 1), cfixture.NewSpan(0, 0),
					cfixture.NewSpan(0, 0), cfixture.NewSpan(0, 0),
					true, "beyond",
				)
				require.NoError(t, err)
				assign.Rules = append(assign.Rules, rule)
				return assign
			},
			want: "a rule names a run out of its array",
		},
		{
			name: "unknown family",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{family: 5})
			},
			want: "a rule of an unknown family",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)

			_, err := cfixture.NewAssignConfig(bed.agent, "config", tc.assign(t))

			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Test_Assign_AnyFamily_TakesTheEarlierRuleOfBothPaths verifies that a rule
// without networks matches packets of either family by protocol and port, and
// that a packet takes whichever of its family's rule and such a rule comes
// first in the ruleset.
func Test_Assign_AnyFamily_TakesTheEarlierRuleOfBothPaths(t *testing.T) {
	forEachBuild(t, assignAnyFamilyTakesTheEarlierRuleOfBothPaths)
}

// assignAnyFamilyTakesTheEarlierRuleOfBothPaths runs the scenario of the test of
// its name over a build.
func assignAnyFamilyTakesTheEarlierRuleOfBothPaths(t *testing.T, build assignBuild) {
	bed := newBed(t)
	inst := bed.assignInstance(build,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10}, 8)},
			ports:   []classify.PortRange{{From: 53, To: 80}},
			pass:    true,
			counter: "v4 low ports",
		},
		assignRule{
			family:  cfixture.AssignFamilyAny,
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}},
			pass:    true,
			counter: "dns",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			counter: "v4 rest",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv6,
			pass:    true,
			counter: "v6 rest",
		},
	)
	bed.serveBeforeForward(inst)
	inner, outside := [4]byte{10, 1, 1, 1}, [4]byte{192, 0, 2, 1}
	frames := [][]byte{
		ip4Frame(inner, outside, protoUDP, 53, 0),
		ip4Frame(outside, inner, protoUDP, 53, 0),
		ip6Frame(addr6(1, 1), addr6(1, 2), protoUDP, 53),
		ip6Frame(addr6(1, 1), addr6(1, 2), protoTCP, 443),
		ip4Frame(outside, inner, protoTCP, 443, 0),
	}

	result := bed.run(0, frames...)

	require.Equal(t,
		[]fate{fateOutput, fateOutput, fateOutput, fateOutput, fateDrop},
		fatesOf(result, frames),
	)
	for _, tc := range []struct {
		name    string
		counter string
		want    []uint64
	}{
		{name: "family rule before the rule of any family", counter: "v4 low ports", want: packetsAndBytes(1, len(frames[0]))},
		{name: "rule of any family before the family rule", counter: "dns", want: packetsAndBytes(2, len(frames[1])+len(frames[2]))},
		{name: "IPv6 family rule alone", counter: "v6 rest", want: packetsAndBytes(1, len(frames[3]))},
		{name: "IPv4 family rule alone", counter: "v4 rest", want: packetsAndBytes(1, len(frames[4]))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, bed.counter(inst, tc.counter)[0])
		})
	}
}

// Test_Assign_Build_RefusedAfterComposing_ReturnsMemoryToBaseline verifies
// that a build refused after it composed every classifier and registered some
// counters gives back all it took.
//
// The last rule's counter name is not UTF-8, which the build reads only when
// it fills the actions, after the classifiers of both families and of any
// family.
func Test_Assign_Build_RefusedAfterComposing_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	assign := assignOf(t,
		assignRule{family: cfixture.AssignFamilyIPv4, src: []any{net4([4]byte{10}, 8)}, counter: "v4"},
		assignRule{family: cfixture.AssignFamilyIPv6, dst: []any{net6(addr6(1, 0), 120)}, counter: "v6"},
		assignRule{family: cfixture.AssignFamilyAny, ports: []classify.PortRange{{From: 53, To: 53}}, counter: "\xff"},
	)

	_, err := cfixture.NewAssignConfig(bed.agent, "config", assign)

	require.ErrorContains(t, err, "a name is not NUL terminated UTF-8")
	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_Assign_NativeBuild_RefusedAfterAllocating_ReturnsMemoryToBaseline
// verifies that the native build refuses a network of another family than its
// rule's as the compile of its side meets it, and a counter name its field
// cannot hold once every classifier is built, and gives back all it took.
func Test_Assign_NativeBuild_RefusedAfterAllocating_ReturnsMemoryToBaseline(t *testing.T) {
	composed := []assignRule{
		{family: cfixture.AssignFamilyIPv4, src: []any{net4([4]byte{10}, 8)}, counter: "v4"},
		{family: cfixture.AssignFamilyIPv6, dst: []any{net6(addr6(1, 0), 120)}, counter: "v6"},
	}
	for _, tc := range []struct {
		name  string
		rules []assignRule
		want  error
	}{
		{
			name:  "IPv6 network of an IPv4 rule",
			rules: []assignRule{{family: cfixture.AssignFamilyIPv4, dst: []any{net6(addr6(1, 0), 120)}}},
			want:  classify.ErrInvalidRule,
		},
		{
			name:  "IPv4 network of an IPv6 rule",
			rules: []assignRule{{family: cfixture.AssignFamilyIPv6, src: []any{net4([4]byte{10}, 8)}}},
			want:  classify.ErrInvalidRule,
		},
		{
			name: "counter name longer than its field after every classifier",
			rules: append(composed, assignRule{
				family:  cfixture.AssignFamilyAny,
				ports:   []classify.PortRange{{From: 53, To: 53}},
				counter: strings.Repeat("c", len(layout.Counter{}.Name)),
			}),
			want: cpconfig.ErrInvalidName,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)

			_, err := buildAssignNative(t, bed, bed.agent, tc.rules)

			require.ErrorIs(t, err, tc.want)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_Assign_Build_MemoryRunsOut_ReturnsMemoryToBaseline verifies that an
// assign build whose memory runs out refuses and gives back all it took, at
// every capacity the sweep tries up to the first that fits.
//
// The rules take every path (IPv4 networks, protocols and ports, IPv6
// networks, rules of any family), so the refusals fall in the attribute
// compiles, the joins and the decode. A capacity stops a build only where the
// buddy allocator runs dry, so a failure between two small allocations of one
// routine is out of a sweep's reach.
func Test_Assign_Build_MemoryRunsOut_ReturnsMemoryToBaseline(t *testing.T) {
	forEachBuild(t, assignBuildMemoryRunsOutReturnsMemoryToBaseline)
}

// assignBuildMemoryRunsOutReturnsMemoryToBaseline runs the scenario of the test of
// its name over a build.
func assignBuildMemoryRunsOutReturnsMemoryToBaseline(t *testing.T, build assignBuild) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(512 * datasize.MB)
	})
	rules := []assignRule{
		{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10}, 8), net4([4]byte{192, 0, 2}, 24)},
			dst:     []any{net4([4]byte{198, 51, 100}, 24)},
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}, {From: 1000, To: 2000}},
			counter: "v4",
		},
		{
			family:  cfixture.AssignFamilyIPv6,
			src:     []any{net6(addr6(1, 0), 120), net6(addr6(2, 0), 64)},
			dst:     []any{net6(addr6(3, 0), 96)},
			counter: "v6",
		},
		{
			family:  cfixture.AssignFamilyAny,
			protos:  []classify.LineRange{{From: protoTCP, To: protoTCP}},
			ports:   []classify.PortRange{{From: 443, To: 443}},
			counter: "any",
		},
	}
	buildOver := func(agent *ffi.Agent) (moduleConfig, error) {
		return build(t, bed, agent, rules)
	}

	fits := sweep(t, bed, buildOver, 0, 16<<20, 16<<10)

	require.NotZero(t, fits, "the build fits within the sweep")
}

// Test_Assign_Build_FailedCompile_ReleasesItsStage verifies that a compile
// that fails right after it allocated its stage's rule groups gives them back.
//
// The rule groups and the next request of the compile are both four bytes per
// rule, so with enough rules they are larger than anything the build asked
// for before. With smaller blocks plenty, one block of their size and none
// larger, the rule groups take the block and the next request fails, a
// failure C leaves to its caller to release the stage after.
func Test_Assign_Build_FailedCompile_ReleasesItsStage(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	agent, err := bed.harness.SharedMemory().AgentAttach("stage", 0, 64*datasize.MB)
	require.NoError(t, err)
	const ruleCount = 32 << 10
	rules := make([]assignRule, ruleCount)
	for idx := range rules {
		rules[idx] = assignRule{family: cfixture.AssignFamilyIPv6, counter: fmt.Sprintf("rule %d", idx)}
	}
	assign := assignOf(t, rules...)
	groups := dataplaneut.AllocatorPoolOf(4 * ruleCount)
	baseline := agent.BlockAllocatorFreeSize()
	hold, err := dataplaneut.HoldAllocator(agent)
	require.NoError(t, err)
	for pool := range groups {
		require.NoError(t, hold.Give(pool, 64, heldFill))
	}
	require.NoError(t, hold.Give(groups, 1, heldFill))

	before := dataplaneut.PoolAllocations(agent)
	_, err = cfixture.NewAssignConfig(agent, "config", assign)
	after := dataplaneut.PoolAllocations(agent)
	hold.Release()

	require.ErrorContains(t, err, "the shared memory is exhausted")
	require.Equal(t, uint64(1), after[groups]-before[groups], "the rule groups take the one block")
	require.Equal(t, baseline, agent.BlockAllocatorFreeSize())
}

// Test_Assign_Build_EachLargeRequestRefused_ReturnsMemoryToBaseline verifies
// that a build refused at any of its large requests, in memory that holds
// stale bytes, gives back all it took and frees nothing it did not allocate.
//
// The IPv6 rule's networks need more trie chunks than the compiles before it
// free, so the sweep fails the IPv6 commit while it builds its first trie,
// where C frees the classifier's other fields whatever they hold.
func Test_Assign_Build_EachLargeRequestRefused_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	agent, err := bed.harness.SharedMemory().AgentAttach("large", 0, 128*datasize.MB)
	require.NoError(t, err)
	var nets6 []any
	for idx := range 32 {
		addr := [16]byte{0x20, 0x01, byte(idx), byte(idx * 7), byte(idx * 13), byte(idx * 31), 7: 1}
		nets6 = append(nets6, net6(addr, 64))
	}
	assign := assignOf(t,
		assignRule{family: cfixture.AssignFamilyIPv4, src: []any{net4([4]byte{10}, 8)}, counter: "v4"},
		assignRule{family: cfixture.AssignFamilyIPv6, src: nets6, counter: "v6"},
		assignRule{family: cfixture.AssignFamilyAny, ports: []classify.PortRange{{From: 53, To: 53}}, counter: "any"},
	)
	build := func(agent *ffi.Agent) (*cfixture.ModuleConfig, error) {
		return cfixture.NewAssignConfig(agent, "config", assign)
	}
	sweepLargeRequests(t, agent, build)
}
