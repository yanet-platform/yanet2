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

// aclRule is the part of an ACL rule the IPv4 ratelimit attributes read.
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

// ipv4Ruleset is the IPv4 family of an ACL ruleset as the ratelimit
// classifier reads it: per rule its source and destination networks,
// its protocols and its destination ports, a rule without an IPv4
// network on either side absent from all four.
type ipv4Ruleset struct {
	Name  string
	Src   []classify.Net4Rule
	Dst   []classify.Net4Rule
	Proto []classify.LineRule
	Port  []classify.PortRule
}

// loadProductionRulesets reads the rulesets productionRulesets names, or
// skips the test without them.
func loadProductionRulesets(t testing.TB) []ipv4Ruleset {
	t.Helper()
	paths := os.Getenv(productionRulesets)
	if paths == "" {
		t.Skipf("set %s to measure on production-shaped rulesets", productionRulesets)
	}
	var rulesets []ipv4Ruleset
	for path := range strings.SplitSeq(paths, ":") {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var document struct {
			Rules []aclRule
		}
		require.NoError(t, json.Unmarshal(data, &document))
		ruleset := ipv4Ruleset{Name: filepath.Base(path)}
		for _, rule := range document.Rules {
			src, dst := ipv4Prefixes(t, rule.Srcs), ipv4Prefixes(t, rule.Dsts)
			if len(src) == 0 || len(dst) == 0 {
				ruleset.Src = append(ruleset.Src, classify.Net4Rule{Absent: true})
				ruleset.Dst = append(ruleset.Dst, classify.Net4Rule{Absent: true})
				ruleset.Proto = append(ruleset.Proto, classify.LineRule{Absent: true})
				ruleset.Port = append(ruleset.Port, classify.PortRule{Absent: true})
				continue
			}
			ruleset.Src = append(ruleset.Src, classify.Net4Rule{Prefixes: src})
			ruleset.Dst = append(ruleset.Dst, classify.Net4Rule{Prefixes: dst})
			var proto classify.LineRule
			// An ACL protocol range spans the protocol in its high byte.
			for _, span := range rule.ProtoRanges {
				proto.Ranges = append(proto.Ranges, classify.LineRange{From: span.From >> 8, To: span.To >> 8})
			}
			ruleset.Proto = append(ruleset.Proto, proto)
			var port classify.PortRule
			for _, span := range rule.DstPortRanges {
				port.Ranges = append(port.Ranges, classify.PortRange{From: uint16(span.From), To: uint16(span.To)})
			}
			ruleset.Port = append(ruleset.Port, port)
		}
		rulesets = append(rulesets, ruleset)
	}
	return rulesets
}

// ipv4Prefixes returns the IPv4 networks among addresses, a bare address
// a host network.
func ipv4Prefixes(t testing.TB, addresses []string) []netip.Prefix {
	t.Helper()
	var prefixes []netip.Prefix
	for _, address := range addresses {
		if strings.Contains(address, ":") {
			continue
		}
		if !strings.Contains(address, "/") {
			address += "/32"
		}
		prefix, err := netip.ParsePrefix(address)
		require.NoError(t, err)
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// Test_ProductionShaped_Sizes logs the sizes the IPv4 ratelimit
// classifier of every production-shaped ruleset reaches.
func Test_ProductionShaped_Sizes(t *testing.T) {
	for idx, ruleset := range loadProductionRulesets(t) {
		present := 0
		for _, rule := range ruleset.Src {
			if !rule.Absent {
				present++
			}
		}
		src, err := classify.CompileNet4(ruleset.Src)
		require.NoError(t, err)
		dst, err := classify.CompileNet4(ruleset.Dst)
		require.NoError(t, err)
		proto := classify.CompileLine(ruleset.Proto, classify.LineDomain)
		port := classify.CompilePorts(ruleset.Port)
		t.Logf("ruleset %d: %d rules, %d IPv4; src %d values, dst %d, proto %d, port %d",
			idx, len(ruleset.Src), present, src.Stage.MaxValue+1, dst.Stage.MaxValue+1, proto.Stage.MaxValue+1, port.Stage.MaxValue+1)
	}
}

// Test_ProductionShaped_MatchesC verifies that the IPv4 ratelimit
// classifier of every production-shaped ruleset compiles and joins in Go
// to what it compiles and joins to in C.
func Test_ProductionShaped_MatchesC(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		requireNet4MatchesC(t, ruleset.Src)
		requireNet4MatchesC(t, ruleset.Dst)
		requireLineMatchesC(t, ruleset.Proto)
		requirePortsMatchC(t, ruleset.Port)
		src, dst, proto, port := compileIPv4(t, ruleset)
		requireJoinMatchesC(t, src.Stage, dst.Stage)
		requireJoinMatchesC(t, proto.Stage, port.Stage)
	}
}

// compileIPv4 compiles the four attributes of a ruleset natively.
func compileIPv4(t testing.TB, ruleset ipv4Ruleset) (*classify.Net4, *classify.Net4, *classify.Line, *classify.Port) {
	t.Helper()
	src, err := classify.CompileNet4(ruleset.Src)
	require.NoError(t, err)
	dst, err := classify.CompileNet4(ruleset.Dst)
	require.NoError(t, err)
	return src, dst, classify.CompileLine(ruleset.Proto, classify.LineDomain), classify.CompilePorts(ruleset.Port)
}

// productionAttribute is one attribute of the IPv4 ratelimit classifier:
// its rules for C, and its compile, commit and free natively.
type productionAttribute struct {
	name   string
	c      func() *coracle.CRules
	native func(b testing.TB, context *layout.MemoryContext)
}

// productionAttributes returns the four attributes of a ruleset.
func productionAttributes(ruleset ipv4Ruleset) []productionAttribute {
	nets := func(rules []classify.Net4Rule, name string) func(b testing.TB, context *layout.MemoryContext) {
		return func(b testing.TB, context *layout.MemoryContext) {
			compiled, err := classify.CompileNet4(rules)
			require.NoError(b, err)
			attr := newAttr[layout.ClassifyAttrNet4](b, context)
			require.NoError(b, compiled.Commit(attr, context, name))
			classify.FreeNet4(attr)
		}
	}
	return []productionAttribute{
		{
			name:   "src",
			c:      func() *coracle.CRules { return coracle.NewCNet4Rules(cNet4Rules(ruleset.Src)) },
			native: nets(ruleset.Src, "ratelimit:src"),
		},
		{
			name:   "dst",
			c:      func() *coracle.CRules { return coracle.NewCNet4Rules(cNet4Rules(ruleset.Dst)) },
			native: nets(ruleset.Dst, "ratelimit:dst"),
		},
		{
			name: "proto",
			c:    func() *coracle.CRules { return coracle.NewCLineRules(cLineRules(ruleset.Proto)) },
			native: func(b testing.TB, context *layout.MemoryContext) {
				line := classify.CompileLine(ruleset.Proto, classify.LineDomain)
				attr := newAttr[layout.ClassifyAttrLine](b, context)
				require.NoError(b, line.Commit(attr, context, "ratelimit:proto"))
				classify.FreeLine(attr)
			},
		},
		{
			name: "port",
			c:    func() *coracle.CRules { return coracle.NewCPortRules(cPortRules(ruleset.Port)) },
			native: func(b testing.TB, context *layout.MemoryContext) {
				ports := classify.CompilePorts(ruleset.Port)
				attr := newAttr[layout.ClassifyAttrPort](b, context)
				require.NoError(b, ports.Commit(attr, context, "ratelimit:port"))
				classify.FreePort(attr)
			},
		},
	}
}

// Benchmark_ProductionShaped measures every compile and join of the IPv4
// ratelimit classifier of every production-shaped ruleset, each with its
// commit and free, through the C compile of the module SDK and natively.
func Benchmark_ProductionShaped(b *testing.B) {
	for _, ruleset := range loadProductionRulesets(b) {
		for _, attribute := range productionAttributes(ruleset) {
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
		src, dst, proto, port := compileIPv4(b, ruleset)
		joins := []struct {
			name        string
			left, right *classify.Stage
		}{
			{"nets", &src.Stage, &dst.Stage},
			{"transport", &proto.Stage, &port.Stage},
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
// compile and join of the IPv4 ratelimit classifier of every
// production-shaped ruleset writes less memory natively, Go heap and
// shared memory together, than in C, shared memory and heap together.
func Test_ProductionShaped_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, ruleset := range loadProductionRulesets(t) {
		for _, attribute := range productionAttributes(ruleset) {
			viaC := measureFootprint(t, func(context *layout.MemoryContext) {
				cRules := attribute.c()
				defer cRules.Free()
				_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:"+attribute.name, 1)
				require.NoError(t, err)
			})
			native := measureFootprint(t, func(context *layout.MemoryContext) {
				attribute.native(t, context)
			})
			t.Logf("%s %s: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
				ruleset.Name, attribute.name, viaC.Shared, native.Shared, native.Heap)
			require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%s %s", ruleset.Name, attribute.name)
		}
		src, dst, proto, port := compileIPv4(t, ruleset)
		joins := []struct {
			name        string
			left, right *classify.Stage
		}{
			{"nets", &src.Stage, &dst.Stage},
			{"transport", &proto.Stage, &port.Stage},
		}
		for _, join := range joins {
			stages := newContext(t)
			cLeft, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(join.left))
			require.NoError(t, err)
			cRight, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(join.right))
			require.NoError(t, err)
			viaC := measureFootprint(t, func(context *layout.MemoryContext) {
				_, err := coracle.JoinLoop(unsafe.Pointer(context), "ratelimit:"+join.name, cLeft, cRight, 1)
				require.NoError(t, err)
			})
			cLeft.Free()
			cRight.Free()
			native := measureFootprint(t, func(context *layout.MemoryContext) {
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
