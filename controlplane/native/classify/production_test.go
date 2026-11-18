//go:build linux && (amd64 || arm64)

package classify_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// productionRulesets names the ACL rulesets, JSON files separated by
// colons, the production-shaped measures read; they skip without it.
const productionRulesets = "YANET_PRODUCTION_RULESETS"

// aclRule is the part of an ACL rule the ratelimit attributes read.
type aclRule struct {
	Srcs          []string
	Dsts          []string
	ProtoRanges   []aclRange `json:"proto_ranges"`
	DstPortRanges []aclRange `json:"dst_port_ranges"`
}

type aclRange struct {
	From uint32
	To   uint32
}

// productionRuleset is an ACL ruleset as the ratelimit classifiers of
// both families read it: per rule its networks of the family, its
// protocols and its destination ports, a rule without a network of the
// family on either side absent from all four attributes of the family.
type productionRuleset struct {
	Name   string
	Src4   []classify.Net4Rule
	Dst4   []classify.Net4Rule
	Proto4 []classify.LineRule
	Port4  []classify.PortRule
	Src6   []classify.Net6Rule
	Dst6   []classify.Net6Rule
	Proto6 []classify.LineRule
	Port6  []classify.PortRule
}

// loadProductionRulesets reads the rulesets productionRulesets names, or
// skips the test without them.
func loadProductionRulesets(t testing.TB) []productionRuleset {
	t.Helper()
	paths := os.Getenv(productionRulesets)
	if paths == "" {
		t.Skipf("set %s to measure on production-shaped rulesets", productionRulesets)
	}
	var rulesets []productionRuleset
	for path := range strings.SplitSeq(paths, ":") {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var document struct {
			Rules []aclRule
		}
		require.NoError(t, json.Unmarshal(data, &document))
		ruleset := productionRuleset{Name: filepath.Base(path)}
		// Rules spelling one address list share one slice of its networks,
		// as a producer hands its lists out.
		lists4 := map[string][]netip.Prefix{}
		lists6 := map[string][]classify.Net6Network{}
		for _, rule := range document.Rules {
			proto, port := aclTransport(rule)
			src4, dst4 := shared(lists4, rule.Srcs, ipv4Prefixes), shared(lists4, rule.Dsts, ipv4Prefixes)
			if len(src4) == 0 || len(dst4) == 0 {
				ruleset.Src4 = append(ruleset.Src4, classify.Net4Rule{Absent: true})
				ruleset.Dst4 = append(ruleset.Dst4, classify.Net4Rule{Absent: true})
				ruleset.Proto4 = append(ruleset.Proto4, classify.LineRule{Absent: true})
				ruleset.Port4 = append(ruleset.Port4, classify.PortRule{Absent: true})
			} else {
				ruleset.Src4 = append(ruleset.Src4, classify.Net4Rule{Prefixes: src4})
				ruleset.Dst4 = append(ruleset.Dst4, classify.Net4Rule{Prefixes: dst4})
				ruleset.Proto4 = append(ruleset.Proto4, proto)
				ruleset.Port4 = append(ruleset.Port4, port)
			}
			src6, dst6 := shared(lists6, rule.Srcs, ipv6Networks), shared(lists6, rule.Dsts, ipv6Networks)
			if len(src6) == 0 || len(dst6) == 0 {
				ruleset.Src6 = append(ruleset.Src6, classify.Net6Rule{Absent: true})
				ruleset.Dst6 = append(ruleset.Dst6, classify.Net6Rule{Absent: true})
				ruleset.Proto6 = append(ruleset.Proto6, classify.LineRule{Absent: true})
				ruleset.Port6 = append(ruleset.Port6, classify.PortRule{Absent: true})
			} else {
				ruleset.Src6 = append(ruleset.Src6, classify.Net6Rule{Networks: src6})
				ruleset.Dst6 = append(ruleset.Dst6, classify.Net6Rule{Networks: dst6})
				ruleset.Proto6 = append(ruleset.Proto6, proto)
				ruleset.Port6 = append(ruleset.Port6, port)
			}
		}
		rulesets = append(rulesets, ruleset)
	}
	return rulesets
}

// aclTransport returns the protocols and the destination ports of a
// rule; an ACL protocol range spans the protocol in its high byte.
func aclTransport(rule aclRule) (classify.LineRule, classify.PortRule) {
	var proto classify.LineRule
	for _, span := range rule.ProtoRanges {
		proto.Ranges = append(proto.Ranges, classify.LineRange{From: span.From >> 8, To: span.To >> 8})
	}
	var port classify.PortRule
	for _, span := range rule.DstPortRanges {
		port.Ranges = append(port.Ranges, classify.PortRange{From: uint16(span.From), To: uint16(span.To)})
	}
	return proto, port
}

// shared returns the networks of an address list, parsed once a list and
// shared by every rule spelling it.
func shared[T any](lists map[string][]T, addresses []string, parse func([]string) []T) []T {
	key := strings.Join(addresses, " ")
	list, ok := lists[key]
	if !ok {
		list = parse(addresses)
		lists[key] = list
	}
	return list
}

// ipv4Prefixes returns the IPv4 networks among addresses, a bare address
// a host network.
func ipv4Prefixes(addresses []string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, address := range addresses {
		if strings.Contains(address, ":") {
			continue
		}
		if !strings.Contains(address, "/") {
			address += "/32"
		}
		prefixes = append(prefixes, netip.MustParsePrefix(address))
	}
	return prefixes
}

// ipv6Networks returns the IPv6 networks among addresses: a prefix, a
// bare address as a host network, or an address and a mask spelled as
// an address.
func ipv6Networks(addresses []string) []classify.Net6Network {
	var networks []classify.Net6Network
	for _, address := range addresses {
		if !strings.Contains(address, ":") {
			continue
		}
		addr, mask, found := strings.Cut(address, "/")
		var network classify.Net6Network
		network.Addr = netip.MustParseAddr(addr).As16()
		switch {
		case !found:
			for idx := range network.Mask {
				network.Mask[idx] = 0xff
			}
		case strings.Contains(mask, ":"):
			network.Mask = netip.MustParseAddr(mask).As16()
		default:
			for bit := range netip.MustParsePrefix(address).Bits() {
				network.Mask[bit/8] |= 0x80 >> (bit % 8)
			}
		}
		networks = append(networks, network)
	}
	return networks
}

// productionAttribute is one attribute of a ratelimit classifier: its
// check against C, its rules for C, its compile, commit and free
// natively, and its stage.
type productionAttribute struct {
	name    string
	matches func(t *testing.T)
	c       func() *coracle.CRules
	native  func(t testing.TB, context *layout.MemoryContext)
	stage   func(t testing.TB) *classify.Stage
}

// productionAttributes returns the attributes of both families of a
// ruleset.
func productionAttributes(ruleset productionRuleset) []productionAttribute {
	net4 := func(name string, rules []classify.Net4Rule) productionAttribute {
		return productionAttribute{
			name:    name,
			matches: func(t *testing.T) { requireNet4MatchesC(t, rules) },
			c:       func() *coracle.CRules { return coracle.NewCNet4Rules(cNet4Rules(rules)) },
			native: func(t testing.TB, context *layout.MemoryContext) {
				compiled, err := classify.CompileNet4(rules)
				require.NoError(t, err)
				attr := newAttr[layout.ClassifyAttrNet4](t, context)
				require.NoError(t, compiled.Commit(attr, context, "ratelimit:"+name))
				classify.FreeNet4(attr)
			},
			stage: func(t testing.TB) *classify.Stage {
				compiled, err := classify.CompileNet4(rules)
				require.NoError(t, err)
				return &compiled.Stage
			},
		}
	}
	net6 := func(name string, rules []classify.Net6Rule) productionAttribute {
		return productionAttribute{
			name:    name,
			matches: func(t *testing.T) { requireNet6MatchesC(t, rules) },
			c:       func() *coracle.CRules { return coracle.NewCNet6Rules(cNet6Rules(rules)) },
			native: func(t testing.TB, context *layout.MemoryContext) {
				attr := newAttr[layout.ClassifyAttrNet6](t, context)
				_, err := classify.CompileNet6(rules, attr, context, "ratelimit:"+name)
				require.NoError(t, err)
				classify.FreeNet6(attr)
			},
			stage: func(t testing.TB) *classify.Stage {
				context := newContext(t)
				attr := newAttr[layout.ClassifyAttrNet6](t, context)
				stage, err := classify.CompileNet6(rules, attr, context, "ratelimit:"+name)
				require.NoError(t, err)
				classify.FreeNet6(attr)
				return stage
			},
		}
	}
	proto := func(name string, rules []classify.LineRule) productionAttribute {
		return productionAttribute{
			name:    name,
			matches: func(t *testing.T) { requireLineMatchesC(t, rules) },
			c:       func() *coracle.CRules { return coracle.NewCLineRules(cLineRules(rules)) },
			native: func(t testing.TB, context *layout.MemoryContext) {
				line := classify.CompileLine(rules, classify.LineDomain)
				attr := newAttr[layout.ClassifyAttrLine](t, context)
				require.NoError(t, line.Commit(attr, context, "ratelimit:"+name))
				classify.FreeLine(attr)
			},
			stage: func(testing.TB) *classify.Stage {
				return &classify.CompileLine(rules, classify.LineDomain).Stage
			},
		}
	}
	port := func(name string, rules []classify.PortRule) productionAttribute {
		return productionAttribute{
			name:    name,
			matches: func(t *testing.T) { requirePortsMatchC(t, rules) },
			c:       func() *coracle.CRules { return coracle.NewCPortRules(cPortRules(rules)) },
			native: func(t testing.TB, context *layout.MemoryContext) {
				ports := classify.CompilePorts(rules)
				attr := newAttr[layout.ClassifyAttrPort](t, context)
				require.NoError(t, ports.Commit(attr, context, "ratelimit:"+name))
				classify.FreePort(attr)
			},
			stage: func(testing.TB) *classify.Stage {
				return &classify.CompilePorts(rules).Stage
			},
		}
	}
	return []productionAttribute{
		net4("src4", ruleset.Src4),
		net4("dst4", ruleset.Dst4),
		proto("proto4", ruleset.Proto4),
		port("port4", ruleset.Port4),
		net6("src6", ruleset.Src6),
		net6("dst6", ruleset.Dst6),
		proto("proto6", ruleset.Proto6),
		port("port6", ruleset.Port6),
	}
}

// productionJoin is one join of a ratelimit classifier: its name and its
// two stages.
type productionJoin struct {
	name        string
	left, right *classify.Stage
}

// productionJoins returns the joins of both families of a ruleset: the
// networks of each family and the transport of each.
func productionJoins(t testing.TB, attributes []productionAttribute) []productionJoin {
	t.Helper()
	stage := func(idx int) *classify.Stage {
		return attributes[idx].stage(t)
	}
	return []productionJoin{
		{"nets4", stage(0), stage(1)},
		{"transport4", stage(2), stage(3)},
		{"nets6", stage(4), stage(5)},
		{"transport6", stage(6), stage(7)},
	}
}

// productionRuleTable is the rule table of one family of a ratelimit
// classifier: its name, the joint of its networks, the transport stage
// and the member rules of the family.
type productionRuleTable struct {
	name    string
	left    *classify.Joint
	right   *classify.Stage
	members []bool
}

// productionRuleTables returns the rule tables of both families of a
// ruleset, over its joins in the order productionJoins returns them.
func productionRuleTables(t testing.TB, ruleset productionRuleset, joins []productionJoin) []productionRuleTable {
	t.Helper()
	members4 := make([]bool, len(ruleset.Src4))
	for idx, rule := range ruleset.Src4 {
		members4[idx] = !rule.Absent
	}
	members6 := make([]bool, len(ruleset.Src6))
	for idx, rule := range ruleset.Src6 {
		members6[idx] = !rule.Absent
	}
	return []productionRuleTable{
		{"rules4", joint(t, joins[0].left, joins[0].right), joint(t, joins[1].left, joins[1].right).Stage(), members4},
		{"rules6", joint(t, joins[2].left, joins[2].right), joint(t, joins[3].left, joins[3].right).Stage(), members6},
	}
}

// Test_ProductionShaped_Sizes logs the sizes the ratelimit classifiers
// of every production-shaped ruleset reach.
func Test_ProductionShaped_Sizes(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		attributes := productionAttributes(ruleset)
		for _, attribute := range attributes {
			stage := attribute.stage(t)
			present := 0
			for _, group := range stage.RuleGroups {
				if group != classify.InvalidGroup {
					present++
				}
			}
			t.Logf("%s %s: %d rules, %d present, %d groups, %d values",
				ruleset.Name, attribute.name, len(stage.RuleGroups), present, stage.GroupCount(), stage.MaxValue+1)
		}
	}
}

// Test_ProductionShaped_InputSizes logs the networks of every
// production-shaped ruleset as rules reference them and as they are
// stored, each shared list once.
func Test_ProductionShaped_InputSizes(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		referenced4, stored4 := storedNetworks(ruleset.Src4, func(rule classify.Net4Rule) []netip.Prefix { return rule.Prefixes })
		referenced6, stored6 := storedNetworks(ruleset.Src6, func(rule classify.Net6Rule) []classify.Net6Network { return rule.Networks })
		dstReferenced4, dstStored4 := storedNetworks(ruleset.Dst4, func(rule classify.Net4Rule) []netip.Prefix { return rule.Prefixes })
		dstReferenced6, dstStored6 := storedNetworks(ruleset.Dst6, func(rule classify.Net6Rule) []classify.Net6Network { return rule.Networks })
		t.Logf("%s: IPv4 sources %d networks referenced, %d stored; destinations %d, %d", ruleset.Name, referenced4, stored4, dstReferenced4, dstStored4)
		t.Logf("%s: IPv6 sources %d networks referenced, %d stored; destinations %d, %d", ruleset.Name, referenced6, stored6, dstReferenced6, dstStored6)
	}
}

// storedNetworks returns how many networks the rules reference and how
// many their lists store, each list sharing its storage counted once.
func storedNetworks[R, T any](rules []R, list func(R) []T) (int, int) {
	seen := map[*T]int{}
	referenced, stored := 0, 0
	for _, rule := range rules {
		networks := list(rule)
		referenced += len(networks)
		if len(networks) == 0 {
			continue
		}
		data := unsafe.SliceData(networks)
		if length, ok := seen[data]; !ok || length < len(networks) {
			stored += len(networks) - length
			seen[data] = len(networks)
		}
	}
	return referenced, stored
}

// Test_ProductionShaped_MatchesC verifies that the ratelimit classifiers
// of every production-shaped ruleset compile and join in Go to what they
// compile and join to in C.
func Test_ProductionShaped_MatchesC(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		attributes := productionAttributes(ruleset)
		for _, attribute := range attributes {
			attribute.matches(t)
		}
		joins := productionJoins(t, attributes)
		for _, join := range joins {
			requireJoinMatchesC(t, *join.left, *join.right)
		}
		for _, rules := range productionRuleTables(t, ruleset, joins) {
			requireRuleTableMatchesC(t, rules.left, rules.right, rules.members)
		}
	}
}

// Benchmark_ProductionShaped measures every compile and join of the
// ratelimit classifiers of every production-shaped ruleset, each with
// its commit and free, through the C compile of the module SDK and
// natively.
func Benchmark_ProductionShaped(b *testing.B) {
	for _, ruleset := range loadProductionRulesets(b) {
		attributes := productionAttributes(ruleset)
		for _, attribute := range attributes {
			b.Run(ruleset.Name+"/"+attribute.name+"/c", func(b *testing.B) {
				context := newContext(b)
				cRules := attribute.c()
				defer cRules.Free()
				b.ResetTimer()
				_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:"+attribute.name, b.N)
				require.NoError(b, err)
			})
			b.Run(ruleset.Name+"/"+attribute.name+"/native", func(b *testing.B) {
				context := newContext(b)
				for b.Loop() {
					attribute.native(b, context)
				}
			})
		}
		joins := productionJoins(b, attributes)
		for _, rules := range productionRuleTables(b, ruleset, joins) {
			b.Run(ruleset.Name+"/"+rules.name+"/c", func(b *testing.B) {
				context := newContext(b)
				cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(rules.left.Stage()))
				require.NoError(b, err)
				defer cLeft.Free()
				cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(rules.right))
				require.NoError(b, err)
				defer cRight.Free()
				b.ResetTimer()
				_, err = coracle.JoinRulesLoop(unsafe.Pointer(context), "ratelimit:"+rules.name, cLeft, cRight, rules.members, b.N)
				require.NoError(b, err)
			})
			b.Run(ruleset.Name+"/"+rules.name+"/native", func(b *testing.B) {
				context := newContext(b)
				table := newAttr[layout.RuleTable](b, context)
				for b.Loop() {
					require.NoError(b, classify.JoinRules(rules.left, rules.right, rules.members, table, context, "ratelimit:"+rules.name))
					classify.FreeRuleTable(table)
				}
			})
		}
		for _, join := range joins {
			b.Run(ruleset.Name+"/"+join.name+"/c", func(b *testing.B) {
				context := newContext(b)
				cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(join.left))
				require.NoError(b, err)
				defer cLeft.Free()
				cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(join.right))
				require.NoError(b, err)
				defer cRight.Free()
				b.ResetTimer()
				_, err = coracle.JoinLoop(unsafe.Pointer(context), "ratelimit:"+join.name, cLeft, cRight, b.N)
				require.NoError(b, err)
			})
			b.Run(ruleset.Name+"/"+join.name+"/native", func(b *testing.B) {
				context := newContext(b)
				table := newAttr[layout.ValueTable](b, context)
				for b.Loop() {
					_, err := classify.Join(join.left, join.right, table, context, "ratelimit:"+join.name)
					require.NoError(b, err)
					classify.FreeJoin(table)
				}
			})
		}
	}
}

// Test_ProductionShaped_UsesLessTemporaryMemoryThanC verifies that every
// compile and join of the ratelimit classifiers of every
// production-shaped ruleset writes less memory natively, Go heap and
// shared memory together, than in C, shared memory and heap together.
func Test_ProductionShaped_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		attributes := productionAttributes(ruleset)
		for _, attribute := range attributes {
			// The rules are copied to C before the measure, as the native
			// compile gets them ready in Go.
			cRules := attribute.c()
			defer cRules.Free()
			viaC := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:"+attribute.name, 1)
				require.NoError(t, err)
			})
			native := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				attribute.native(t, context)
			})
			t.Logf("%s %s: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
				ruleset.Name, attribute.name, viaC.Shared, native.Shared, native.Heap)
			require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%s %s", ruleset.Name, attribute.name)
		}
		joins := productionJoins(t, attributes)
		for _, rules := range productionRuleTables(t, ruleset, joins) {
			stages := newContext(t)
			cLeft, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(rules.left.Stage()))
			require.NoError(t, err)
			cRight, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(rules.right))
			require.NoError(t, err)
			viaC := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				_, err := coracle.JoinRulesLoop(unsafe.Pointer(context), "ratelimit:"+rules.name, cLeft, cRight, rules.members, 1)
				require.NoError(t, err)
			})
			cLeft.Free()
			cRight.Free()
			native := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				table := newAttr[layout.RuleTable](t, context)
				require.NoError(t, classify.JoinRules(rules.left, rules.right, rules.members, table, context, "ratelimit:"+rules.name))
				classify.FreeRuleTable(table)
			})
			t.Logf("%s %s: C %d bytes of shared memory and %d of heap; native %d of shared memory and %d of Go heap",
				ruleset.Name, rules.name, viaC.Shared, viaC.Heap, native.Shared, native.Heap)
			require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%s %s", ruleset.Name, rules.name)
		}
		for _, join := range joins {
			stages := newContext(t)
			cLeft, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(join.left))
			require.NoError(t, err)
			cRight, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(join.right))
			require.NoError(t, err)
			viaC := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				_, err := coracle.JoinLoop(unsafe.Pointer(context), "ratelimit:"+join.name, cLeft, cRight, 1)
				require.NoError(t, err)
			})
			cLeft.Free()
			cRight.Free()
			native := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
				table := newAttr[layout.ValueTable](t, context)
				_, err := classify.Join(join.left, join.right, table, context, "ratelimit:"+join.name)
				require.NoError(t, err)
				classify.FreeJoin(table)
			})
			t.Logf("%s %s: C %d bytes of shared memory and %d of heap; native %d of shared memory and %d of Go heap",
				ruleset.Name, join.name, viaC.Shared, viaC.Heap, native.Shared, native.Heap)
			require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%s %s", ruleset.Name, join.name)
		}
	}
}
