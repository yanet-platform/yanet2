//go:build sdk_rust_fixtures

package dataplane_test

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
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
type assignRule struct {
	family   sdkfixture.AssignFamily
	src, dst []netip.Prefix
	protos   []classify.LineRange
	ports    []classify.PortRange
	pass     bool
	counter  string
}

// buildAssign builds an assign configuration named config of the rules over
// the agent, without registering it for cleanup.
func buildAssign(owner *agent.Agent, rules []assignRule) (*moduleConfig, error) {
	native := make([]sdkfixture.AssignRule, 0, len(rules))
	for _, rule := range rules {
		native = append(native, sdkfixture.AssignRule{
			Family:  rule.family,
			Src:     rule.src,
			Dst:     rule.dst,
			Protos:  rule.protos,
			Ports:   rule.ports,
			Pass:    rule.pass,
			Counter: rule.counter,
		})
	}
	module, err := sdkfixture.NewAssignConfig(owner, "config", native)
	return moduleHandle(module, err, sdkfixture.FreeAssignConfig)
}

// assignInstance builds an assign configuration of the rules as an instance
// of the module type assign.
func (m *bed) assignInstance(rules ...assignRule) *instance {
	m.t.Helper()

	config, err := buildAssign(m.agent, rules)
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
	bed := newBed(t)
	inst := bed.assignInstance(
		assignRule{
			family:  sdkfixture.AssignIPv4,
			src:     []netip.Prefix{net4([4]byte{10}, 8)},
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}},
			counter: "dns",
		},
		assignRule{
			family:  sdkfixture.AssignIPv4,
			src:     []netip.Prefix{net4([4]byte{10, 1}, 16)},
			dst:     []netip.Prefix{net4([4]byte{192, 0, 2}, 24)},
			pass:    true,
			counter: "inner",
		},
		assignRule{
			family:  sdkfixture.AssignIPv4,
			src:     []netip.Prefix{net4([4]byte{10, 1}, 16)},
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
	bed := newBed(t)
	inst := bed.assignInstance(
		assignRule{
			family:  sdkfixture.AssignIPv6,
			src:     []netip.Prefix{net6(addr6(1, 0), 120)},
			ports:   []classify.PortRange{{From: 1000, To: 2000}},
			pass:    true,
			counter: "v6 ports",
		},
		assignRule{
			family:  sdkfixture.AssignIPv4,
			pass:    true,
			counter: "v4 any",
		},
		assignRule{
			family:  sdkfixture.AssignIPv6,
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
	bed := newBed(t)
	inst := bed.assignInstance(
		assignRule{
			family:  sdkfixture.AssignIPv4,
			ports:   []classify.PortRange{{From: 53, To: 53}},
			pass:    true,
			counter: "dns",
		},
		assignRule{
			family:  sdkfixture.AssignIPv4,
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
// the compile would read as other than it says.
func Test_Assign_Build_RefusesInvalidRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule assignRule
	}{
		{
			name: "port range that ends before it starts",
			rule: assignRule{
				family: sdkfixture.AssignIPv4,
				ports:  []classify.PortRange{{From: 80, To: 53}},
			},
		},
		{
			name: "protocol range that ends before it starts",
			rule: assignRule{
				family: sdkfixture.AssignIPv6,
				protos: []classify.LineRange{{From: 17, To: 6}},
			},
		},
		{
			name: "unknown family",
			rule: assignRule{family: 5},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)

			_, err := buildAssign(bed.agent, []assignRule{tc.rule})

			require.ErrorIs(t, err, classify.ErrInvalidRule)
		})
	}
}

// Test_Assign_AnyFamily_TakesTheEarlierRuleOfBothPaths verifies that a rule
// without networks matches packets of either family by protocol and port, and
// that a packet takes whichever of its family's rule and such a rule comes
// first in the ruleset.
func Test_Assign_AnyFamily_TakesTheEarlierRuleOfBothPaths(t *testing.T) {
	bed := newBed(t)
	inst := bed.assignInstance(
		assignRule{
			family:  sdkfixture.AssignIPv4,
			src:     []netip.Prefix{net4([4]byte{10}, 8)},
			ports:   []classify.PortRange{{From: 53, To: 80}},
			pass:    true,
			counter: "v4 low ports",
		},
		assignRule{
			family:  sdkfixture.AssignAny,
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}},
			pass:    true,
			counter: "dns",
		},
		assignRule{
			family:  sdkfixture.AssignIPv4,
			counter: "v4 rest",
		},
		assignRule{
			family:  sdkfixture.AssignIPv6,
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

// Test_Assign_Build_RefusedAfterAllocating_ReturnsMemoryToBaseline verifies
// that the build refuses a network of another family than its rule's as the
// compile of its side meets it, and a counter name its field cannot hold once
// every classifier is built, and gives back all it took.
func Test_Assign_Build_RefusedAfterAllocating_ReturnsMemoryToBaseline(t *testing.T) {
	composed := []assignRule{
		{family: sdkfixture.AssignIPv4, src: []netip.Prefix{net4([4]byte{10}, 8)}, counter: "v4"},
		{family: sdkfixture.AssignIPv6, dst: []netip.Prefix{net6(addr6(1, 0), 120)}, counter: "v6"},
	}
	for _, tc := range []struct {
		name  string
		rules []assignRule
		want  error
	}{
		{
			name:  "IPv6 network of an IPv4 rule",
			rules: []assignRule{{family: sdkfixture.AssignIPv4, dst: []netip.Prefix{net6(addr6(1, 0), 120)}}},
			want:  classify.ErrInvalidRule,
		},
		{
			name:  "IPv4 network of an IPv6 rule",
			rules: []assignRule{{family: sdkfixture.AssignIPv6, src: []netip.Prefix{net4([4]byte{10}, 8)}}},
			want:  classify.ErrInvalidRule,
		},
		{
			name: "counter name longer than its field after every classifier",
			rules: append(composed, assignRule{
				family:  sdkfixture.AssignAny,
				ports:   []classify.PortRange{{From: 53, To: 53}},
				counter: strings.Repeat("c", len(layout.Counter{}.Name)),
			}),
			want: cpconfig.ErrInvalidName,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)

			_, err := buildAssign(bed.agent, tc.rules)

			require.ErrorIs(t, err, tc.want)
			require.Equal(t, bed.baseline, freeSize(bed.agent))
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
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(512 * datasize.MB)
	})
	rules := []assignRule{
		{
			family:  sdkfixture.AssignIPv4,
			src:     []netip.Prefix{net4([4]byte{10}, 8), net4([4]byte{192, 0, 2}, 24)},
			dst:     []netip.Prefix{net4([4]byte{198, 51, 100}, 24)},
			protos:  []classify.LineRange{{From: protoUDP, To: protoUDP}},
			ports:   []classify.PortRange{{From: 53, To: 53}, {From: 1000, To: 2000}},
			counter: "v4",
		},
		{
			family:  sdkfixture.AssignIPv6,
			src:     []netip.Prefix{net6(addr6(1, 0), 120), net6(addr6(2, 0), 64)},
			dst:     []netip.Prefix{net6(addr6(3, 0), 96)},
			counter: "v6",
		},
		{
			family:  sdkfixture.AssignAny,
			protos:  []classify.LineRange{{From: protoTCP, To: protoTCP}},
			ports:   []classify.PortRange{{From: 443, To: 443}},
			counter: "any",
		},
	}
	buildOver := func(owner *agent.Agent) (*moduleConfig, error) {
		return buildAssign(owner, rules)
	}

	fits := sweep(t, bed, buildOver, 0, 16<<20, 16<<10)

	require.NotZero(t, fits, "the build fits within the sweep")
}

// Test_Assign_Build_EachLargeRequestRefused_ReturnsMemoryToBaseline verifies
// that a build refused at any of its allocations gives back all it took and
// frees nothing it did not allocate.
//
// The IPv6 rule's networks need more trie chunks than the compiles before it
// free, so the sweep fails the IPv6 commit while it builds its first trie,
// where the classifier's other fields are freed whatever they hold.
func Test_Assign_Build_EachLargeRequestRefused_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	owner := attach(t, bed.instance, "large", 128*datasize.MB)
	var nets6 []netip.Prefix
	for idx := range 32 {
		addr := [16]byte{0x20, 0x01, byte(idx), byte(idx * 7), byte(idx * 13), byte(idx * 31), 7: 1}
		nets6 = append(nets6, net6(addr, 64))
	}
	rules := []assignRule{
		{family: sdkfixture.AssignIPv4, src: []netip.Prefix{net4([4]byte{10}, 8)}, counter: "v4"},
		{family: sdkfixture.AssignIPv6, src: nets6, counter: "v6"},
		{family: sdkfixture.AssignAny, ports: []classify.PortRange{{From: 53, To: 53}}, counter: "any"},
	}

	sweepAllocations(t, owner, func(owner *agent.Agent) (func() error, error) {
		config, err := buildAssign(owner, rules)
		if err != nil {
			return nil, err
		}
		return config.Free, nil
	})
}
