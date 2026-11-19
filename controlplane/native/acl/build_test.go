//go:build linux && (amd64 || arm64)

package acl_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/acl"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// productionRulesets names the ACL rulesets, JSON files separated by
// colons, the production-shaped comparison reads; it skips without it.
const productionRulesets = "YANET_PRODUCTION_RULESETS"

// aclFixture is a dataplane with the acl module and two agents: "c",
// which the C api builds through, and "owner", which Go builds through.
type aclFixture struct {
	*nativetest.Fixture
	cAgent *ffi.Agent
	owner  *agent.Agent
}

// newACLFixture returns a fixture whose agents hold agentSize bytes each.
func newACLFixture(t testing.TB, agentSize datasize.ByteSize) *aclFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		CPMemory:      uint64(2*agentSize + 256*datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	})
	cAgent, err := memory.Harness.SharedMemory().AgentAttach("c", 0, agentSize)
	require.NoError(t, err)
	_, err = memory.Harness.SharedMemory().AgentAttach("owner", 0, agentSize)
	require.NoError(t, err)
	owner, ok := agent.Find(memory.Instance(), "owner")
	require.True(t, ok)
	return &aclFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// requireMatchesC builds the rules through the C api and through Go and
// requires the two configurations to hold the same tables, targets,
// counters and links.
func requireMatchesC(t *testing.T, memory *aclFixture, rules []cacl.ACLRule, fw4, fw6 string) {
	t.Helper()
	handle, err := cacl.NewModuleConfig(memory.cAgent, "acl0", rules, fw4, fw6)
	require.NoError(t, err)
	defer func() { require.NoError(t, handle.Free()) }()
	module, err := acl.NewConfig(memory.owner, "acl0", rules, fw4, fw6)
	require.NoError(t, err)
	defer func() { require.NoError(t, acl.Free(module)) }()

	want := (*layout.AclModuleConfig)(handle.AsFFIModule().AsRawPtr())
	got := (*layout.AclModuleConfig)(unsafe.Pointer(module))
	requireSameConfig(t, want, got)
}

// requireSameConfig requires two ACL configurations to hold the same
// content, whatever the addresses it lies at.
func requireSameConfig(t *testing.T, want, got *layout.AclModuleConfig) {
	t.Helper()
	requireSameModule(t, &want.CpModule, &got.CpModule)

	requireSameDevice(t, "l2:device", &want.ClassifierL2.DevAttr, &got.ClassifierL2.DevAttr)
	requireSameVline(t, "l2:rules", &want.FilterL2.RuleMap, &got.FilterL2.RuleMap)

	core4w, core4g := &want.ClassifierCore4, &got.ClassifierCore4
	requireSameDevice(t, "ip4:device", &core4w.DevAttr, &core4g.DevAttr)
	requireSameNet4(t, "ip4:net4_src", &core4w.Net4SrcAttr, &core4g.Net4SrcAttr)
	requireSameNet4(t, "ip4:net4_dst", &core4w.Net4DstAttr, &core4g.Net4DstAttr)
	requireSameVline(t, "ip4:ipproto", &core4w.IpprotoAttr.Line, &core4g.IpprotoAttr.Line)
	requireSameTable(t, "ip4:nets_joint", &core4w.NetsJoint, &core4g.NetsJoint)
	requireSameTable(t, "ip4:mid_joint", &core4w.MidJoint, &core4g.MidJoint)
	requireSameTable(t, "ip4:proto_joint", &core4w.ProtoJoint, &core4g.ProtoJoint)
	requireSameTable(t, "ip4:ipfrag", &want.ClassifierFrag4.FragAttr.ValueTable, &got.ClassifierFrag4.FragAttr.ValueTable)
	requireSameTable(t, "ip4:root_joint", &want.FilterIp4.RootJoint, &got.FilterIp4.RootJoint)
	requireSameVline(t, "ip4:rules", &want.FilterIp4.RuleMap, &got.FilterIp4.RuleMap)
	requireSamePaths(t, "ip4",
		paths{&want.ClassifierPorts4, &want.ClassifierTcp4, &want.ClassifierIcmp4,
			&want.FilterIp4Tcp.RootJoint, &want.FilterIp4Tcp.VacJoint, &want.FilterIp4Udp.RootJoint,
			&want.FilterIp4Udp.VacRuleMap, &want.FilterIp4Icmp.RootJoint},
		paths{&got.ClassifierPorts4, &got.ClassifierTcp4, &got.ClassifierIcmp4,
			&got.FilterIp4Tcp.RootJoint, &got.FilterIp4Tcp.VacJoint, &got.FilterIp4Udp.RootJoint,
			&got.FilterIp4Udp.VacRuleMap, &got.FilterIp4Icmp.RootJoint},
	)

	core6w, core6g := &want.ClassifierCore6, &got.ClassifierCore6
	requireSameDevice(t, "ip6:device", &core6w.DevAttr, &core6g.DevAttr)
	requireSameNet6(t, "ip6:net6_src", &core6w.Net6SrcAttr, &core6g.Net6SrcAttr)
	requireSameNet6(t, "ip6:net6_dst", &core6w.Net6DstAttr, &core6g.Net6DstAttr)
	requireSameVline(t, "ip6:ipproto", &core6w.IpprotoAttr.Line, &core6g.IpprotoAttr.Line)
	requireSameTable(t, "ip6:nets_joint", &core6w.NetsJoint, &core6g.NetsJoint)
	requireSameTable(t, "ip6:mid_joint", &core6w.MidJoint, &core6g.MidJoint)
	requireSameTable(t, "ip6:proto_joint", &core6w.ProtoJoint, &core6g.ProtoJoint)
	requireSameTable(t, "ip6:ipfrag", &want.ClassifierFrag6.FragAttr.ValueTable, &got.ClassifierFrag6.FragAttr.ValueTable)
	requireSameTable(t, "ip6:root_joint", &want.FilterIp6.RootJoint, &got.FilterIp6.RootJoint)
	requireSameVline(t, "ip6:rules", &want.FilterIp6.RuleMap, &got.FilterIp6.RuleMap)
	requireSamePaths(t, "ip6",
		paths{&want.ClassifierPorts6, &want.ClassifierTcp6, &want.ClassifierIcmp6,
			&want.FilterIp6Tcp.RootJoint, &want.FilterIp6Tcp.VacJoint, &want.FilterIp6Udp.RootJoint,
			&want.FilterIp6Udp.VacRuleMap, &want.FilterIp6Icmp.RootJoint},
		paths{&got.ClassifierPorts6, &got.ClassifierTcp6, &got.ClassifierIcmp6,
			&got.FilterIp6Tcp.RootJoint, &got.FilterIp6Tcp.VacJoint, &got.FilterIp6Udp.RootJoint,
			&got.FilterIp6Udp.VacRuleMap, &got.FilterIp6Icmp.RootJoint},
	)

	require.Equal(t, want.TargetCount, got.TargetCount, "target count")
	wantTargets := unsafe.Slice(want.Targets.Get(), want.TargetCount)
	gotTargets := unsafe.Slice(got.Targets.Get(), got.TargetCount)
	for idx := range wantTargets {
		count := wantTargets[idx].ActionCount
		require.Equal(t, count, gotTargets[idx].ActionCount, "action count of target %d", idx)
		require.Equal(t, wantTargets[idx].Actions[:count], gotTargets[idx].Actions[:count], "actions of target %d", idx)
		require.Equal(t, wantTargets[idx].CounterId, gotTargets[idx].CounterId, "counter of target %d", idx)
	}
	require.Zero(t, got.AbsTargets, "dataplane target pointer")

	for _, scalar := range []struct {
		name      string
		want, got uint64
	}{
		{"rules registry", want.RulesRegistryIdx, got.RulesRegistryIdx},
		{"fwstate v4 link", want.V4ObjectLinkIdx, got.V4ObjectLinkIdx},
		{"fwstate v6 link", want.V6ObjectLinkIdx, got.V6ObjectLinkIdx},
		{"l2 rules", want.FilterRuleCountL2, got.FilterRuleCountL2},
		{"ip4 rules", want.FilterRuleCountIp4, got.FilterRuleCountIp4},
		{"ip4 tcp rules", want.FilterRuleCountIp4Tcp, got.FilterRuleCountIp4Tcp},
		{"ip4 udp rules", want.FilterRuleCountIp4Udp, got.FilterRuleCountIp4Udp},
		{"ip4 icmp rules", want.FilterRuleCountIp4Icmp, got.FilterRuleCountIp4Icmp},
		{"ip6 rules", want.FilterRuleCountIp6, got.FilterRuleCountIp6},
		{"ip6 tcp rules", want.FilterRuleCountIp6Tcp, got.FilterRuleCountIp6Tcp},
		{"ip6 udp rules", want.FilterRuleCountIp6Udp, got.FilterRuleCountIp6Udp},
		{"ip6 icmp rules", want.FilterRuleCountIp6Icmp, got.FilterRuleCountIp6Icmp},
		{"no match counter", want.NoMatchCounterId, got.NoMatchCounterId},
		{"allow counter", want.ActionAllowCounterId, got.ActionAllowCounterId},
		{"deny counter", want.ActionDenyCounterId, got.ActionDenyCounterId},
		{"check pass counter", want.ActionCheckPassCounterId, got.ActionCheckPassCounterId},
		{"check miss counter", want.ActionCheckMissCounterId, got.ActionCheckMissCounterId},
		{"create state counter", want.ActionCreateStateCounterId, got.ActionCreateStateCounterId},
		{"invalid counter", want.ActionInvalidCounterId, got.ActionInvalidCounterId},
		{"non term counter", want.ActionNonTermCounterId, got.ActionNonTermCounterId},
		{"sync sent counter", want.SyncSentCounterId, got.SyncSentCounterId},
		{"sync overflow counter", want.SyncOverflowCounterId, got.SyncOverflowCounterId},
	} {
		require.Equal(t, scalar.want, scalar.got, scalar.name)
	}
}

// paths are the tables of a family's protocol paths.
type paths struct {
	Ports     *layout.AclClassifierPorts
	TCP       *layout.AclClassifierTcp
	ICMP      *layout.AclClassifierIcmp
	TCPRoot   *layout.RuleTable
	TCPVac    *layout.RuleTable
	UDPRoot   *layout.RuleTable
	UDPVacMap *layout.Vline
	ICMPRoot  *layout.RuleTable
}

// requireSamePaths requires two families' protocol paths to hold the
// same tables.
func requireSamePaths(t *testing.T, family string, want, got paths) {
	t.Helper()
	requireSameVline(t, family+":src_port", &want.Ports.SrcAttr.Line, &got.Ports.SrcAttr.Line)
	requireSameVline(t, family+":dst_port", &want.Ports.DstAttr.Line, &got.Ports.DstAttr.Line)
	requireSameTable(t, family+":ports_joint", &want.Ports.Joint, &got.Ports.Joint)
	requireSameVline(t, family+":tcp_flags", &want.TCP.FlagsAttr.Line, &got.TCP.FlagsAttr.Line)
	requireSameTable(t, family+":flags_joint", &want.TCP.FlagsJoint, &got.TCP.FlagsJoint)
	requireSameVline(t, family+":icmp_type", &want.ICMP.TypeAttr.Line, &got.ICMP.TypeAttr.Line)
	requireSameRuleTable(t, family+":tcp_root_joint", want.TCPRoot, got.TCPRoot)
	requireSameRuleTable(t, family+":tcp_vac_joint", want.TCPVac, got.TCPVac)
	requireSameRuleTable(t, family+":udp_root_joint", want.UDPRoot, got.UDPRoot)
	requireSameVline(t, family+":udp_vac_rules", want.UDPVacMap, got.UDPVacMap)
	requireSameRuleTable(t, family+":icmp_root_joint", want.ICMPRoot, got.ICMPRoot)
}

// contextName returns the name of a memory context, empty for none.
func contextName(context *layout.MemoryContext) string {
	if context == nil {
		return ""
	}
	return shm.String(context.Name[:])
}

// requireSameVline requires two lines to hold the same values in
// memory contexts of the same name.
func requireSameVline(t *testing.T, label string, want, got *layout.Vline) {
	t.Helper()
	require.Equal(t, contextName(want.MemoryContext.Get()), contextName(got.MemoryContext.Get()), "%s: context", label)
	require.Equal(t, want.Size, got.Size, "%s: size", label)
	if want.Values.Get() == nil {
		require.Nil(t, got.Values.Get(), "%s: values", label)
		return
	}
	requireSameCells(t, label+": values", [][]uint32{unsafe.Slice(want.Values.Get(), want.Size)}, [][]uint32{unsafe.Slice(got.Values.Get(), got.Size)})
}

// requireSameCells requires two tables of cells to be equal, failing at
// the first differing cell: production-shaped tables are too large for
// a full diff.
func requireSameCells(t *testing.T, label string, want, got [][]uint32) {
	t.Helper()
	require.Equal(t, len(want), len(got), "%s: rows", label)
	for row := range want {
		if slices.Equal(want[row], got[row]) {
			continue
		}
		require.Equal(t, len(want[row]), len(got[row]), "%s: cells of row %d", label, row)
		for column := range want[row] {
			require.Equal(t, want[row][column], got[row][column], "%s: cell %d of row %d", label, column, row)
		}
	}
}

// requireSameDevice requires two device attributes to hold the same
// line.
func requireSameDevice(t *testing.T, label string, want, got *layout.ClassifyAttrDevice) {
	t.Helper()
	requireSameVline(t, label, &want.Line, &got.Line)
}

// tableRows returns the rows of a value table, none for a table without
// them.
func tableRows(table *layout.ValueTable) [][]uint32 {
	if table.Values.Get() == nil {
		return nil
	}
	rows := unsafe.Slice(table.Values.Get(), table.VDim)
	cells := make([][]uint32, table.VDim)
	for v := range rows {
		cells[v] = unsafe.Slice(rows[v].Get(), table.HDim)
	}
	return cells
}

// requireSameTable requires two value tables to hold the same cells in
// memory contexts of the same name.
func requireSameTable(t *testing.T, label string, want, got *layout.ValueTable) {
	t.Helper()
	require.Equal(t, contextName(want.MemoryContext.Get()), contextName(got.MemoryContext.Get()), "%s: context", label)
	require.Equal(t, [2]uint32{want.VDim, want.HDim}, [2]uint32{got.VDim, got.HDim}, "%s: dimensions", label)
	requireSameCells(t, label+": cells", tableRows(want), tableRows(got))
}

// requireSameRuleTable requires two rule tables to hold the same class
// map and rows in memory contexts of the same names.
func requireSameRuleTable(t *testing.T, label string, want, got *layout.RuleTable) {
	t.Helper()
	require.Equal(t, contextName(want.MemoryContext.Get()), contextName(got.MemoryContext.Get()), "%s: context", label)
	require.Equal(t, [3]uint32{want.VDim, want.HDim, want.ClassCount}, [3]uint32{got.VDim, got.HDim, got.ClassCount}, "%s: dimensions", label)
	requireSameVline(t, label+" classes", &want.ClassMap, &got.ClassMap)
	ruleRows := func(table *layout.RuleTable) [][]uint32 {
		if table.Rows.Get() == nil {
			return nil
		}
		pointers := unsafe.Slice(table.Rows.Get(), table.ClassCount)
		rows := make([][]uint32, len(pointers))
		for idx := range pointers {
			rows[idx] = unsafe.Slice(pointers[idx].Get(), table.HDim)
		}
		return rows
	}
	requireSameCells(t, label+": rows", ruleRows(want), ruleRows(got))
}

// requireSameNet4 requires two IPv4 network attributes to answer every
// lookup alike.
func requireSameNet4(t *testing.T, label string, want, got *layout.ClassifyAttrNet4) {
	t.Helper()
	require.Equal(t, shm.String(want.Lpm.MemoryContext.Name[:]), shm.String(got.Lpm.MemoryContext.Name[:]), "%s: context", label)
	require.Equal(t, coracle.Stretches(&want.Lpm, 4), coracle.Stretches(&got.Lpm, 4), "%s: lookups", label)
}

// requireSameNet6 requires two IPv6 network attributes to answer every
// lookup alike: both halves and the table combining them.
func requireSameNet6(t *testing.T, label string, want, got *layout.ClassifyAttrNet6) {
	t.Helper()
	require.Equal(t, shm.String(want.Hi.MemoryContext.Name[:]), shm.String(got.Hi.MemoryContext.Name[:]), "%s: context", label)
	require.Equal(t, coracle.Stretches(&want.Hi, 8), coracle.Stretches(&got.Hi, 8), "%s: high half lookups", label)
	require.Equal(t, coracle.Stretches(&want.Lo, 8), coracle.Stretches(&got.Lo, 8), "%s: low half lookups", label)
	requireSameTable(t, label+" table", &want.Comb, &got.Comb)
}

// counters returns the name, size, offset and flags of every counter a
// registry holds, in index order.
func counters(registry *layout.CounterRegistry) []string {
	if registry.Names.Get() == nil {
		return nil
	}
	names := unsafe.Slice(registry.Names.Get(), registry.Count)
	out := make([]string, len(names))
	for idx := range names {
		out[idx] = fmt.Sprintf("%s/%d/%d/%d", shm.String(names[idx].Name[:]), names[idx].Size, names[idx].Offset, names[idx].Flags)
	}
	return out
}

// requireSameModule requires two module headers to name the same module
// and hold the same devices, object links and counters.
func requireSameModule(t *testing.T, want, got *layout.CpModule) {
	t.Helper()
	require.Equal(t, shm.String(want.Type[:]), shm.String(got.Type[:]), "module type")
	require.Equal(t, shm.String(want.Name[:]), shm.String(got.Name[:]), "module name")
	require.Equal(t, want.DpModuleIdx, got.DpModuleIdx, "dataplane module")
	devices := func(module *layout.CpModule) []string {
		var out []string
		for _, device := range unsafe.Slice(module.Devices.Get(), module.DeviceCount) {
			out = append(out, shm.String(device.Name[:]))
		}
		return out
	}
	require.Equal(t, devices(want), devices(got), "devices")
	objects := func(module *layout.CpModule) []string {
		var out []string
		for _, object := range unsafe.Slice(module.Objects.Get(), module.ObjectCount) {
			out = append(out, shm.String(object.Type[:])+":"+shm.String(object.Name[:]))
		}
		return out
	}
	require.Equal(t, objects(want), objects(got), "object links")
	require.Equal(t, counters(&want.CounterRegistry), counters(&got.CounterRegistry), "module counters")
	registries := func(module *layout.CpModule) map[string][]string {
		out := map[string][]string{}
		entries := unsafe.Slice(module.RuntimeCounterRegistries.Get(), module.RuntimeCounterRegistryCount)
		for idx := range entries {
			registry := entries[idx].Get()
			out[shm.String(registry.Tag[:])] = counters(&registry.Registry)
		}
		return out
	}
	require.Equal(t, registries(want), registries(got), "runtime counter registries")
	require.Equal(t, want.RuntimeCounterRegistryCount, got.RuntimeCounterRegistryCount, "runtime counter registry count")
}

// randomRules returns a reproducible ruleset over every shape the build
// tells apart: devices, families, one-sided and absent networks,
// protocol and port ranges, fragments, actions and counters.
func randomRules(seed uint64, count int) []cacl.ACLRule {
	random := rand.New(rand.NewPCG(seed, 0x9e3779b9))
	nets4 := []string{"10.0.0.0/8", "10.1.0.0/16", "10.1.2.0/24", "192.0.2.7/32", "0.0.0.0/0", "172.16.0.0/12", "10.1.2.128/25"}
	nets6 := []string{"2001:db8::/32", "2001:db8:1::/48", "::/0", "fe80::/10", "2001:db8:1:2::1/128", "2001:db8:0:0:1::/80"}
	devices := []string{"port0", "port1", "port2"}
	counterNames := []string{"", "", "web", "dns", "drop_all"}
	pick4 := func() []xnetip.Contiguous[xnetip.Network4] {
		var out []xnetip.Contiguous[xnetip.Network4]
		for range random.IntN(3) {
			out = append(out, xnetip.MustParseContiguous4(nets4[random.IntN(len(nets4))]))
		}
		return out
	}
	pick6 := func() []xnetip.BiContiguous {
		var out []xnetip.BiContiguous
		for range random.IntN(3) {
			out = append(out, xnetip.MustParseBiContiguous(nets6[random.IntN(len(nets6))]))
		}
		return out
	}
	protos := func() filter.ProtoRanges {
		var out filter.ProtoRanges
		for range random.IntN(3) {
			switch random.IntN(6) {
			case 0:
				out = append(out, filter.ProtoRange{From: 0, To: 0xffff})
			case 1:
				low := uint8(random.IntN(256))
				out = append(out, filter.NewProtoRange(6, filter.RangeSubtype(low, low+uint8(random.IntN(int(255-low)+1)))))
			case 2:
				out = append(out, filter.NewProtoRange(17, filter.AnySubtype()))
			case 3:
				out = append(out, filter.NewProtoRange([]uint8{1, 58}[random.IntN(2)], filter.ExactSubtype(uint8(random.IntN(4)))))
			case 4:
				out = append(out, filter.ProtoRange{From: 6 << 8, To: 17<<8 | 0xff})
			default:
				out = append(out, filter.NewProtoRange(uint8(random.IntN(256)), filter.AnySubtype()))
			}
		}
		return out
	}
	ports := func() filter.PortRanges {
		var out filter.PortRanges
		switch random.IntN(4) {
		case 0:
		case 1:
			out = append(out, filter.PortRange{From: 0, To: 65535})
		default:
			for range 1 + random.IntN(2) {
				from := uint16(random.IntN(2000))
				out = append(out, filter.PortRange{From: from, To: from + uint16(random.IntN(100))})
			}
		}
		return out
	}
	kinds := []uint32{
		uint32(cacl.ActionAllow), uint32(cacl.ActionDeny), uint32(cacl.ActionCount),
		uint32(cacl.ActionCheckState), uint32(cacl.ActionCreateState), uint32(cacl.ActionLog),
	}
	rules := make([]cacl.ACLRule, count)
	for idx := range rules {
		rule := &rules[idx]
		for range random.IntN(3) {
			rule.Devices = append(rule.Devices, filter.Device{Name: devices[random.IntN(len(devices))]})
		}
		switch random.IntN(5) {
		case 0:
		case 1:
			rule.Src4s, rule.Dst4s = pick4(), pick4()
		case 2:
			rule.Src6s, rule.Dst6s = pick6(), pick6()
		default:
			rule.Src4s, rule.Dst4s = pick4(), pick4()
			rule.Src6s, rule.Dst6s = pick6(), pick6()
		}
		rule.ProtoRanges = protos()
		rule.SrcPortRanges = ports()
		rule.DstPortRanges = ports()
		rule.Fragment = []filter.Fragment{filter.FragmentAny, filter.FragmentNone, filter.FragmentFrag}[random.IntN(3)]
		for range 1 + random.IntN(3) {
			rule.Actions = append(rule.Actions, cacl.ACLAction{Kind: kinds[random.IntN(len(kinds))]})
		}
		rule.Counter = counterNames[random.IntN(len(counterNames))]
	}
	return rules
}

// Test_NewConfig_MatchesC verifies that an ACL configuration Go builds
// holds the classifiers, filters, targets, counters and links the C api
// builds from the same rules.
func Test_NewConfig_MatchesC(t *testing.T) {
	cases := []struct {
		name     string
		rules    []cacl.ACLRule
		fw4, fw6 string
	}{
		{name: "no rules"},
		{name: "one rule of every family", rules: randomRules(1, 3)},
		{name: "fwstate links", rules: randomRules(2, 8), fw4: "fw4", fw6: "fw6"},
	}
	for seed := range uint64(40) {
		cases = append(cases, struct {
			name     string
			rules    []cacl.ACLRule
			fw4, fw6 string
		}{name: fmt.Sprintf("random ruleset %d", seed), rules: randomRules(100+seed, 20+int(seed)*5)})
	}
	memory := newACLFixture(t, 256*datasize.MB)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireMatchesC(t, memory, tc.rules, tc.fw4, tc.fw6)
		})
	}
}

// Test_NewConfig_ProductionShapedMatchesC verifies that the ACL
// configurations Go builds from production-shaped rulesets hold what the
// C api builds from them.
func Test_NewConfig_ProductionShapedMatchesC(t *testing.T) {
	paths := os.Getenv(productionRulesets)
	if paths == "" {
		t.Skipf("set %s to compare on production-shaped rulesets", productionRulesets)
	}
	memory := newACLFixture(t, 12*datasize.GB)
	for path := range strings.SplitSeq(paths, ":") {
		t.Run(path, func(t *testing.T) {
			requireMatchesC(t, memory, loadRules(t, path), "", "")
		})
	}
}

// datasetRule is one rule of a ruleset dump the ACL CLI accepts.
type datasetRule struct {
	Actions []struct {
		Kind string `json:"kind"`
	} `json:"actions"`
	Counter string `json:"counter"`
	Devices []struct {
		Name string `json:"name"`
	} `json:"devices"`
	Srcs          []string                    `json:"srcs"`
	Dsts          []string                    `json:"dsts"`
	ProtoRanges   []struct{ From, To uint16 } `json:"proto_ranges"`
	SrcPortRanges []struct{ From, To uint16 } `json:"src_port_ranges"`
	DstPortRanges []struct{ From, To uint16 } `json:"dst_port_ranges"`
	Fragment      struct {
		Kind uint32 `json:"kind"`
	} `json:"fragment"`
}

// loadRules reads a ruleset dump into the rules the C api takes.
func loadRules(t *testing.T, path string) []cacl.ACLRule {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var document struct {
		Rules []datasetRule `json:"rules"`
	}
	require.NoError(t, json.Unmarshal(data, &document))
	kinds := map[string]uint32{
		"ACTION_KIND_PASS":         uint32(cacl.ActionAllow),
		"ACTION_KIND_DENY":         uint32(cacl.ActionDeny),
		"ACTION_KIND_COUNT":        uint32(cacl.ActionCount),
		"ACTION_KIND_CHECK_STATE":  uint32(cacl.ActionCheckState),
		"ACTION_KIND_CREATE_STATE": uint32(cacl.ActionCreateState),
		"ACTION_KIND_LOG":          uint32(cacl.ActionLog),
	}
	rules := make([]cacl.ACLRule, len(document.Rules))
	for idx, raw := range document.Rules {
		rule := &rules[idx]
		rule.Counter = raw.Counter
		rule.Fragment = filter.Fragment(raw.Fragment.Kind)
		for _, action := range raw.Actions {
			kind, ok := kinds[action.Kind]
			require.True(t, ok, "action kind %q", action.Kind)
			rule.Actions = append(rule.Actions, cacl.ACLAction{Kind: kind})
		}
		for _, device := range raw.Devices {
			rule.Devices = append(rule.Devices, filter.Device{Name: device.Name})
		}
		for _, side := range []struct {
			addresses []string
			nets4     *[]xnetip.Contiguous[xnetip.Network4]
			nets6     *[]xnetip.BiContiguous
		}{{raw.Srcs, &rule.Src4s, &rule.Src6s}, {raw.Dsts, &rule.Dst4s, &rule.Dst6s}} {
			for _, address := range side.addresses {
				if strings.Contains(address, ":") {
					network, err := xnetip.ParseNetwork6(address)
					require.NoError(t, err)
					typed, ok := xnetip.BiContiguousFrom6(network)
					require.True(t, ok, "IPv6 mask of %s", address)
					*side.nets6 = append(*side.nets6, typed)
					continue
				}
				network, err := xnetip.ParseNetwork4(address)
				require.NoError(t, err)
				typed, ok := xnetip.ContiguousFrom(network)
				require.True(t, ok, "IPv4 mask of %s", address)
				*side.nets4 = append(*side.nets4, typed)
			}
		}
		for _, span := range raw.ProtoRanges {
			rule.ProtoRanges = append(rule.ProtoRanges, filter.ProtoRange{From: span.From, To: span.To})
		}
		for _, span := range raw.SrcPortRanges {
			rule.SrcPortRanges = append(rule.SrcPortRanges, filter.PortRange{From: span.From, To: span.To})
		}
		for _, span := range raw.DstPortRanges {
			rule.DstPortRanges = append(rule.DstPortRanges, filter.PortRange{From: span.From, To: span.To})
		}
	}
	return rules
}
