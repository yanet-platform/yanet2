//go:build sdk_rust_fixtures

package cfixture_test

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

const (
	protoTCP = 6
	protoUDP = 17
)

// assignRule is a rule of an assign test: its networks, protocols and ports,
// each empty for any, and what it does.
type assignRule struct {
	family   uint64
	src, dst []any
	protos   []cfixture.LineRange
	ports    []cfixture.PortRange
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
				assign.Nets4 = append(assign.Nets4, item.(cfixture.Net4))
			}
			return cfixture.NewSpan(uint64(offset), uint64(len(items)))
		}
		offset := len(assign.Nets6)
		for _, item := range items {
			assign.Nets6 = append(assign.Nets6, item.(cfixture.Net6))
		}
		return cfixture.NewSpan(uint64(offset), uint64(len(items)))
	}
	for _, rule := range rules {
		src, dst := nets(rule.family, rule.src), nets(rule.family, rule.dst)
		protos := cfixture.NewSpan(uint64(len(assign.Lines)), uint64(len(rule.protos)))
		assign.Lines = append(assign.Lines, rule.protos...)
		ports := cfixture.NewSpan(uint64(len(assign.Ports)), uint64(len(rule.ports)))
		assign.Ports = append(assign.Ports, rule.ports...)
		record, err := cfixture.NewAssignRule(rule.family, src, dst, protos, ports, rule.pass, rule.counter)
		require.NoError(t, err)
		assign.Rules = append(assign.Rules, record)
	}
	return assign
}

// assignInstance builds an assign configuration of the rules as an instance
// of the module type assign.
func (m *bed) assignInstance(assign cfixture.Assign) *instance {
	m.t.Helper()

	config, err := cfixture.NewAssignConfig(m.agent, "config", assign)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return &instance{moduleType: "assign", name: "config", config: config}
}

// net4 returns the IPv4 network of the address and prefix length.
func net4(addr [4]byte, bits int) cfixture.Net4 {
	var mask [4]byte
	binary.BigEndian.PutUint32(mask[:], ^uint32(0)<<(32-bits))
	return cfixture.NewNet4(addr, mask)
}

// net6 returns the IPv6 network of the address and prefix length.
func net6(addr [16]byte, bits int) cfixture.Net6 {
	var mask [16]byte
	for idx := range mask {
		switch left := bits - 8*idx; {
		case left >= 8:
			mask[idx] = 0xff
		case left > 0:
			mask[idx] = ^byte(0) << (8 - left)
		}
	}
	return cfixture.NewNet6(addr, mask)
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
	bed := newBed(t)
	inst := bed.assignInstance(assignOf(t,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10}, 8)},
			protos:  []cfixture.LineRange{cfixture.NewLineRange(protoUDP, protoUDP)},
			ports:   []cfixture.PortRange{cfixture.NewPortRange(53, 53)},
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
	))
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
	bed := newBed(t)
	inst := bed.assignInstance(assignOf(t,
		assignRule{
			family:  cfixture.AssignFamilyIPv6,
			src:     []any{net6(addr6(1, 0), 120)},
			ports:   []cfixture.PortRange{cfixture.NewPortRange(1000, 2000)},
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
	))
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
	bed := newBed(t)
	inst := bed.assignInstance(assignOf(t,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			ports:   []cfixture.PortRange{cfixture.NewPortRange(53, 53)},
			pass:    true,
			counter: "dns",
		},
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			ports:   []cfixture.PortRange{cfixture.NewPortRange(0, 0)},
			counter: "port zero",
		},
	))
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
			name: "IPv4 mask that is not a prefix",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv4,
					src:    []any{cfixture.NewNet4([4]byte{10}, [4]byte{0xff, 0, 0xff})},
				})
			},
			want: "a network mask is not a prefix",
		},
		{
			name: "IPv6 mask that is not a prefix in its low half",
			assign: func(t *testing.T) cfixture.Assign {
				mask := [16]byte{0xff, 0xff, 8: 0x0f}
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv6,
					dst:    []any{cfixture.NewNet6(addr6(0, 0), mask)},
				})
			},
			want: "a network mask is not a prefix",
		},
		{
			name: "port range that ends before it starts",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv4,
					ports:  []cfixture.PortRange{cfixture.NewPortRange(80, 53)},
				})
			},
			want: "a range ends before it starts",
		},
		{
			name: "protocol range that ends before it starts",
			assign: func(t *testing.T) cfixture.Assign {
				return assignOf(t, assignRule{
					family: cfixture.AssignFamilyIPv6,
					protos: []cfixture.LineRange{cfixture.NewLineRange(17, 6)},
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
	bed := newBed(t)
	inst := bed.assignInstance(assignOf(t,
		assignRule{
			family:  cfixture.AssignFamilyIPv4,
			src:     []any{net4([4]byte{10}, 8)},
			ports:   []cfixture.PortRange{cfixture.NewPortRange(53, 80)},
			pass:    true,
			counter: "v4 low ports",
		},
		assignRule{
			family:  cfixture.AssignFamilyAny,
			protos:  []cfixture.LineRange{cfixture.NewLineRange(protoUDP, protoUDP)},
			ports:   []cfixture.PortRange{cfixture.NewPortRange(53, 53)},
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
	))
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

// Test_Assign_Build_RefusedMidway_ReturnsMemoryToBaseline verifies that a build
// refused after it composed some of its classifiers gives back all it took.
func Test_Assign_Build_RefusedMidway_ReturnsMemoryToBaseline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []assignRule
	}{
		{
			name: "IPv6 mask refused after the IPv4 classifier",
			rules: []assignRule{
				{family: cfixture.AssignFamilyIPv4, src: []any{net4([4]byte{10}, 8)}, counter: "v4"},
				{
					family:  cfixture.AssignFamilyIPv6,
					src:     []any{cfixture.NewNet6(addr6(0, 0), [16]byte{0x0f})},
					counter: "v6",
				},
			},
		},
		{
			name: "range of any family refused after both family classifiers",
			rules: []assignRule{
				{family: cfixture.AssignFamilyIPv4, counter: "v4"},
				{family: cfixture.AssignFamilyIPv6, counter: "v6"},
				{
					family:  cfixture.AssignFamilyAny,
					ports:   []cfixture.PortRange{cfixture.NewPortRange(2, 1)},
					counter: "any",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)

			_, err := cfixture.NewAssignConfig(bed.agent, "config", assignOf(t, tc.rules...))

			require.Error(t, err)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}
