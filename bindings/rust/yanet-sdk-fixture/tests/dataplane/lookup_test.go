//go:build sdk_rust_fixtures

package dataplane_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"pgregory.net/rapid"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// lookupPacket is a packet a lookup test sends: its family, addresses,
// protocol and destination port, and for IPv4 its fragment offset.
type lookupPacket struct {
	family   sdkfixture.AssignFamily
	src, dst netip.Addr
	proto    byte
	port     uint16
	fragment uint16
}

// frame returns the frame of the packet.
func (m lookupPacket) frame() []byte {
	if m.family == sdkfixture.AssignIPv4 {
		return ip4Frame(m.src.As4(), m.dst.As4(), m.proto, m.port, m.fragment)
	}
	return ip6Frame(m.src.As16(), m.dst.As16(), m.proto, m.port)
}

// matches reports whether the rule matches the packet, as the module reads
// it: by the rule's family, or any family by protocol and port alone, and
// by the destination port only for TCP and UDP, zero for other protocols.
func (m assignRule) matches(packet lookupPacket) bool {
	if m.family != sdkfixture.AssignAny {
		if m.family != packet.family || !inPrefixes(m.src, packet.src) || !inPrefixes(m.dst, packet.dst) {
			return false
		}
	}
	port := uint16(0)
	if packet.proto == protoTCP || packet.proto == protoUDP {
		port = packet.port
	}
	protoOK := len(m.protos) == 0 || slices.ContainsFunc(m.protos, func(lines classify.LineRange) bool {
		return lines.From <= uint32(packet.proto) && uint32(packet.proto) <= lines.To
	})
	portOK := len(m.ports) == 0 || slices.ContainsFunc(m.ports, func(ports classify.PortRange) bool {
		return ports.From <= port && port <= ports.To
	})
	return protoOK && portOK
}

// inPrefixes reports whether the address is in one of the networks, every
// address being in none given.
func inPrefixes(prefixes []netip.Prefix, addr netip.Addr) bool {
	return len(prefixes) == 0 || slices.ContainsFunc(prefixes, func(prefix netip.Prefix) bool {
		return prefix.Contains(addr)
	})
}

// firstMatch returns the index of the first rule that matches the packet,
// or -1 when none does.
func firstMatch(rules []assignRule, packet lookupPacket) int {
	return slices.IndexFunc(rules, func(rule assignRule) bool { return rule.matches(packet) })
}

// lookupBed is a bed serving an assign configuration, which each case
// replaces, before a forwarding instance.
type lookupBed struct {
	*bed
	inst *instance
}

// newLookupBed starts a bed serving an assign configuration of no rules,
// built over an agent of 128 MB.
//
// A property builds and frees a configuration per case, and the allocator
// never merges the blocks it splits, so a smaller agent runs out of large
// blocks over the cases, sooner with the red zones of a sanitizer build.
func newLookupBed(t *testing.T) *lookupBed {
	t.Helper()

	bed := newBed(t, func(config *dataplaneut.Config) {
		config.CPMemory = uint64(256 * datasize.MB)
	})
	bed.agent = attach(t, bed.instance, "lookup", 128*datasize.MB)
	bed.baseline = freeSize(bed.agent)
	inst := bed.assignInstance()
	bed.serveBeforeForward(inst)
	return &lookupBed{bed: bed, inst: inst}
}

// check publishes an assign configuration of the rules, the counter of rule
// n named "rule n", runs the packets and requires each to end as the rule
// want names, -1 for none, says: on the transmit list when the rule passes
// it, on the drop list when it drops it or no rule matches, counted by its
// rule alone.
func (m *lookupBed) check(t require.TestingT, rules []assignRule, packets []lookupPacket, want []int) {
	for idx := range rules {
		rules[idx].counter = fmt.Sprintf("rule %d", idx)
	}
	config, err := buildAssign(m.agent, rules)
	require.NoError(t, err)
	m.freeAtEnd(config)
	replacement := &instance{moduleType: "assign", name: "config", config: config, path: m.inst.path}
	m.publishModules(replacement)
	require.NoError(t, m.inst.config.Free())
	m.inst = replacement

	frames := make([][]byte, 0, len(packets))
	wantFates := make([]fate, 0, len(packets))
	wantCounts := make([]uint64, len(rules))
	for idx, packet := range packets {
		frames = append(frames, packet.frame())
		rule := want[idx]
		switch {
		case rule < 0:
			wantFates = append(wantFates, fateDrop)
		case rules[rule].pass:
			wantFates = append(wantFates, fateOutput)
		default:
			wantFates = append(wantFates, fateDrop)
		}
		if rule >= 0 {
			wantCounts[rule]++
		}
	}
	before := m.ruleCounts(len(rules))
	result := m.run(0, frames...)
	after := m.ruleCounts(len(rules))

	gotCounts := make([]uint64, len(rules))
	for idx := range gotCounts {
		gotCounts[idx] = after[idx] - before[idx]
	}
	require.Equal(t, wantFates, fatesOf(result, frames), "fates of %v", packets)
	require.Equal(t, wantCounts, gotCounts, "packets of each rule of %v", packets)
}

// ruleCounts returns the packets each rule's counter counted on worker 0.
func (m *lookupBed) ruleCounts(rules int) []uint64 {
	m.t.Helper()

	counters := m.counters(pathTags(m.inst, "module")...)
	counts := make([]uint64, 0, rules)
	for idx := range rules {
		counts = append(counts, valuesOf(m.t, counters, fmt.Sprintf("rule %d", idx))[0][0])
	}
	return counts
}

// The pools the lookup properties draw from, small so that the rules they
// draw collide, nest and repeat.
var (
	lookupAddrs4 = []netip.Addr{
		netip.MustParseAddr("0.0.0.0"),
		netip.MustParseAddr("10.0.0.0"),
		netip.MustParseAddr("10.0.0.128"),
		netip.MustParseAddr("10.1.0.0"),
		netip.MustParseAddr("10.1.2.3"),
		netip.MustParseAddr("192.0.2.0"),
		netip.MustParseAddr("192.0.2.128"),
		netip.MustParseAddr("255.255.255.255"),
	}
	lookupBits4  = []int{0, 8, 16, 24, 25, 31, 32}
	lookupAddrs6 = []netip.Addr{
		netip.MustParseAddr("::"),
		netip.MustParseAddr("2001:db8::"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("2001:db8::8000:0:0:0"),
		netip.MustParseAddr("2001:db8:0:1::"),
		netip.MustParseAddr("2001:db8:1::"),
		netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"),
	}
	lookupBits6   = []int{0, 32, 48, 63, 64, 65, 96, 127, 128}
	lookupProtos4 = []byte{1, protoTCP, protoUDP}
	lookupProtos6 = []byte{58, protoTCP, protoUDP}
	lookupPorts   = []uint16{0, 1, 53, 80, 1023, 1024, 8080, 65534, 65535}
)

// drawPrefixes draws up to two networks of the pools.
func drawPrefixes(t *rapid.T, label string, addrs []netip.Addr, bits []int) []netip.Prefix {
	count := rapid.IntRange(0, 2).Draw(t, label+" count")
	prefixes := make([]netip.Prefix, 0, count)
	for range count {
		addr := rapid.SampledFrom(addrs).Draw(t, label)
		prefix, err := addr.Prefix(rapid.SampledFrom(bits).Draw(t, label+" bits"))
		if err != nil {
			panic(err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// drawRanges draws up to two ranges of values of the pool, each a value or
// two values in order.
func drawRanges[V byte | uint16](t *rapid.T, label string, pool []V) [][2]V {
	count := rapid.IntRange(0, 2).Draw(t, label+" count")
	ranges := make([][2]V, 0, count)
	for range count {
		from, to := rapid.SampledFrom(pool).Draw(t, label), rapid.SampledFrom(pool).Draw(t, label)
		if rapid.Bool().Draw(t, label+" single") {
			to = from
		}
		ranges = append(ranges, [2]V{min(from, to), max(from, to)})
	}
	return ranges
}

// drawRule draws a rule of one of the families.
func drawRule(t *rapid.T, families []sdkfixture.AssignFamily) assignRule {
	rule := assignRule{
		family: rapid.SampledFrom(families).Draw(t, "family"),
		pass:   rapid.Bool().Draw(t, "pass"),
	}
	switch rule.family {
	case sdkfixture.AssignIPv4:
		rule.src = drawPrefixes(t, "src", lookupAddrs4, lookupBits4)
		rule.dst = drawPrefixes(t, "dst", lookupAddrs4, lookupBits4)
	case sdkfixture.AssignIPv6:
		rule.src = drawPrefixes(t, "src", lookupAddrs6, lookupBits6)
		rule.dst = drawPrefixes(t, "dst", lookupAddrs6, lookupBits6)
	}
	for _, protos := range drawRanges(t, "protos", append(slices.Clone(lookupProtos4), lookupProtos6...)) {
		rule.protos = append(rule.protos, classify.LineRange{From: uint32(protos[0]), To: uint32(protos[1])})
	}
	for _, ports := range drawRanges(t, "ports", lookupPorts) {
		rule.ports = append(rule.ports, classify.PortRange{From: ports[0], To: ports[1]})
	}
	return rule
}

// edgeAddrs returns the first and the last address of each network of the
// family the rules name, one before and one past each, and the pool's
// addresses of the family.
func edgeAddrs(rules []assignRule, family sdkfixture.AssignFamily) []netip.Addr {
	addrs := slices.Clone(lookupAddrs4)
	if family == sdkfixture.AssignIPv6 {
		addrs = slices.Clone(lookupAddrs6)
	}
	for _, rule := range rules {
		if rule.family != family {
			continue
		}
		for _, prefix := range slices.Concat(rule.src, rule.dst) {
			network, _ := xnetip.NetworkFromPrefix(prefix)
			first, last := prefix.Masked().Addr(), network.LastAddr()
			addrs = append(addrs, first, last)
			if before := first.Prev(); before.IsValid() {
				addrs = append(addrs, before)
			}
			if past := last.Next(); past.IsValid() {
				addrs = append(addrs, past)
			}
		}
	}
	return addrs
}

// edgePorts returns the ends of each port range the rules name, one before
// and one past each, and the pool's ports.
func edgePorts(rules []assignRule) []uint16 {
	ports := slices.Clone(lookupPorts)
	for _, rule := range rules {
		for _, ranges := range rule.ports {
			ports = append(ports, ranges.From, ranges.To)
			if ranges.From > 0 {
				ports = append(ports, ranges.From-1)
			}
			if ranges.To < 65535 {
				ports = append(ports, ranges.To+1)
			}
		}
	}
	return ports
}

// drawPackets draws packets of the families at the edges of the rules'
// networks and port ranges.
func drawPackets(t *rapid.T, rules []assignRule, families []sdkfixture.AssignFamily) []lookupPacket {
	ports := edgePorts(rules)
	packets := make([]lookupPacket, 0, 24)
	for range cap(packets) {
		family := rapid.SampledFrom(families).Draw(t, "packet family")
		addrs := edgeAddrs(rules, family)
		protos := lookupProtos4
		if family == sdkfixture.AssignIPv6 {
			protos = lookupProtos6
		}
		packets = append(packets, lookupPacket{
			family: family,
			src:    rapid.SampledFrom(addrs).Draw(t, "packet src"),
			dst:    rapid.SampledFrom(addrs).Draw(t, "packet dst"),
			proto:  rapid.SampledFrom(protos).Draw(t, "packet proto"),
			port:   rapid.SampledFrom(ports).Draw(t, "packet port"),
		})
	}
	return packets
}

// Test_Assign_Lookup_IsTheNaiveFirstMatch verifies that over random rulesets
// of each family, whose networks, protocols and ports collide, nest and
// repeat, every packet at the edges of their networks and port ranges ends
// and is counted as the first rule it matches says.
//
// The rules of either family come among those of the family, so a packet
// takes the earlier of the rules of both paths; the case of either family
// sends packets of both families to rules of either family alone.
func Test_Assign_Lookup_IsTheNaiveFirstMatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   []sdkfixture.AssignFamily
		packets []sdkfixture.AssignFamily
	}{
		{
			name:    "IPv4",
			rules:   []sdkfixture.AssignFamily{sdkfixture.AssignIPv4, sdkfixture.AssignIPv4, sdkfixture.AssignAny},
			packets: []sdkfixture.AssignFamily{sdkfixture.AssignIPv4},
		},
		{
			name:    "IPv6",
			rules:   []sdkfixture.AssignFamily{sdkfixture.AssignIPv6, sdkfixture.AssignIPv6, sdkfixture.AssignAny},
			packets: []sdkfixture.AssignFamily{sdkfixture.AssignIPv6},
		},
		{
			name:    "either family",
			rules:   []sdkfixture.AssignFamily{sdkfixture.AssignAny},
			packets: []sdkfixture.AssignFamily{sdkfixture.AssignIPv4, sdkfixture.AssignIPv6},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newLookupBed(t)

			rapid.Check(t, func(t *rapid.T) {
				count := rapid.IntRange(1, 6).Draw(t, "rules")
				rules := make([]assignRule, 0, count)
				for range count {
					rules = append(rules, drawRule(t, tc.rules))
				}
				packets := drawPackets(t, rules, tc.packets)
				want := make([]int, 0, len(packets))
				for _, packet := range packets {
					want = append(want, firstMatch(rules, packet))
				}
				bed.check(t, rules, packets, want)
			})
		})
	}
}

// edgePacket is a packet of an edge case and the rule it takes, -1 for none.
type edgePacket struct {
	name   string
	packet lookupPacket
	rule   int
}

// v4Packet returns an IPv4 packet from 192.0.2.1 to the address.
func v4Packet(dst string, proto byte, port uint16) lookupPacket {
	return lookupPacket{
		family: sdkfixture.AssignIPv4,
		src:    netip.MustParseAddr("192.0.2.1"),
		dst:    netip.MustParseAddr(dst),
		proto:  proto,
		port:   port,
	}
}

// v6Packet returns an IPv6 packet from the address to the address.
func v6Packet(src, dst string, proto byte, port uint16) lookupPacket {
	return lookupPacket{
		family: sdkfixture.AssignIPv6,
		src:    netip.MustParseAddr(src),
		dst:    netip.MustParseAddr(dst),
		proto:  proto,
		port:   port,
	}
}

// Test_Assign_Lookup_Edges verifies that a packet at an edge of a rule's
// attributes takes the rule it matches, and none when it is just outside:
// networks of every address and of one, the lowest and the highest port, a
// fragment after the first, an IPv6 network that splits the low half of an
// address, and a rule of either family before the rules of each family.
func Test_Assign_Lookup_Edges(t *testing.T) {
	tcp, udp := []classify.LineRange{{From: protoTCP, To: protoTCP}}, []classify.LineRange{{From: protoUDP, To: protoUDP}}
	for _, tc := range []struct {
		name    string
		rules   []assignRule
		packets []edgePacket
	}{
		{
			name: "network of every IPv4 address",
			rules: []assignRule{{
				family: sdkfixture.AssignIPv4,
				dst:    []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
				protos: tcp,
				ports:  []classify.PortRange{{From: 22, To: 22}},
			}},
			packets: []edgePacket{
				{name: "lowest address", packet: v4Packet("0.0.0.0", protoTCP, 22), rule: 0},
				{name: "highest address", packet: v4Packet("255.255.255.255", protoTCP, 22), rule: 0},
				{name: "other protocol", packet: v4Packet("10.0.0.1", protoUDP, 22), rule: -1},
			},
		},
		{
			name: "network of one IPv4 address",
			rules: []assignRule{{
				family: sdkfixture.AssignIPv4,
				dst:    []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32")},
			}},
			packets: []edgePacket{
				{name: "the address", packet: v4Packet("203.0.113.7", protoUDP, 53), rule: 0},
				{name: "one before", packet: v4Packet("203.0.113.6", protoUDP, 53), rule: -1},
				{name: "one past", packet: v4Packet("203.0.113.8", protoUDP, 53), rule: -1},
			},
		},
		{
			name: "network of one IPv6 address",
			rules: []assignRule{{
				family: sdkfixture.AssignIPv6,
				dst:    []netip.Prefix{netip.MustParsePrefix("2001:db8::1/128")},
				protos: tcp,
				ports:  []classify.PortRange{{From: 1, To: 1024}},
			}},
			packets: []edgePacket{
				{name: "first port", packet: v6Packet("2001:db8::9", "2001:db8::1", protoTCP, 1), rule: 0},
				{name: "last port", packet: v6Packet("2001:db8::9", "2001:db8::1", protoTCP, 1024), rule: 0},
				{name: "port before", packet: v6Packet("2001:db8::9", "2001:db8::1", protoTCP, 0), rule: -1},
				{name: "port past", packet: v6Packet("2001:db8::9", "2001:db8::1", protoTCP, 1025), rule: -1},
				{name: "address before", packet: v6Packet("2001:db8::9", "2001:db8::", protoTCP, 80), rule: -1},
				{name: "address past", packet: v6Packet("2001:db8::9", "2001:db8::2", protoTCP, 80), rule: -1},
			},
		},
		{
			name: "lowest and highest port",
			rules: []assignRule{{
				family: sdkfixture.AssignIPv4,
				ports:  []classify.PortRange{{From: 0, To: 0}, {From: 65535, To: 65535}},
			}},
			packets: []edgePacket{
				{name: "port 0", packet: v4Packet("10.0.0.1", protoUDP, 0), rule: 0},
				{name: "port 65535", packet: v4Packet("10.0.0.1", protoUDP, 65535), rule: 0},
				{name: "port 1", packet: v4Packet("10.0.0.1", protoUDP, 1), rule: -1},
				{name: "port 65534", packet: v4Packet("10.0.0.1", protoUDP, 65534), rule: -1},
			},
		},
		{
			name: "fragment after the first",
			rules: []assignRule{
				{
					family: sdkfixture.AssignIPv4,
					dst:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
					ports:  []classify.PortRange{{From: 53, To: 53}},
				},
				{
					family: sdkfixture.AssignIPv4,
					dst:    []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
					ports:  []classify.PortRange{{From: 0, To: 0}},
				},
			},
			packets: []edgePacket{
				{name: "first fragment by its port", packet: v4Packet("10.0.0.1", protoUDP, 53), rule: 0},
				{
					name: "later fragment by port zero and its networks",
					packet: lookupPacket{
						family:   sdkfixture.AssignIPv4,
						src:      netip.MustParseAddr("192.0.2.1"),
						dst:      netip.MustParseAddr("10.0.0.1"),
						proto:    protoUDP,
						port:     53,
						fragment: 185,
					},
					rule: 1,
				},
			},
		},
		{
			name: "IPv6 network that splits the low half",
			rules: []assignRule{{
				family: sdkfixture.AssignIPv6,
				src:    []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")},
				dst:    []netip.Prefix{netip.MustParsePrefix("2001:db8::8000:0:0:0/65")},
				protos: udp,
				ports:  []classify.PortRange{{From: 53, To: 53}},
			}},
			packets: []edgePacket{
				{name: "upper half of the low half", packet: v6Packet("2001:db8::", "2001:db8::8000:0:0:2", protoUDP, 53), rule: 0},
				{name: "lower half of the low half", packet: v6Packet("2001:db8::", "2001:db8::2", protoUDP, 53), rule: -1},
				{name: "first address", packet: v6Packet("2001:db8::", "2001:db8::8000:0:0:0", protoUDP, 53), rule: 0},
				{name: "one before", packet: v6Packet("2001:db8::", "2001:db8::7fff:ffff:ffff:ffff", protoUDP, 53), rule: -1},
				{name: "last address", packet: v6Packet("2001:db8::", "2001:db8::ffff:ffff:ffff:ffff", protoUDP, 53), rule: 0},
				{name: "one past", packet: v6Packet("2001:db8::", "2001:db8:0:1::", protoUDP, 53), rule: -1},
			},
		},
		{
			name: "rule of either family before each family's",
			rules: []assignRule{
				{family: sdkfixture.AssignAny, protos: udp, ports: []classify.PortRange{{From: 53, To: 53}}},
				{family: sdkfixture.AssignIPv4},
				{family: sdkfixture.AssignIPv6},
			},
			packets: []edgePacket{
				{name: "IPv4 of the earlier rule", packet: v4Packet("10.0.0.1", protoUDP, 53), rule: 0},
				{name: "IPv6 of the earlier rule", packet: v6Packet("2001:db8::9", "2001:db8::1", protoUDP, 53), rule: 0},
				{name: "IPv4 of its family's rule", packet: v4Packet("10.0.0.1", protoTCP, 53), rule: 1},
				{name: "IPv6 of its family's rule", packet: v6Packet("2001:db8::9", "2001:db8::1", protoTCP, 53), rule: 2},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newLookupBed(t)
			for _, edge := range tc.packets {
				t.Run(edge.name, func(t *testing.T) {
					bed.check(t, slices.Clone(tc.rules), []lookupPacket{edge.packet}, []int{edge.rule})
				})
			}
		})
	}
}
