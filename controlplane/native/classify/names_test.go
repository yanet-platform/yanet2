//go:build linux && (amd64 || arm64)

package classify_test

import (
	"net/netip"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// The attribute views of the two rules lib/classify's context_names.c
// composes.
var (
	namesVlanRules = []classify.LineRule{
		{Ranges: []classify.LineRange{{From: 10, To: 10}}},
		{Ranges: []classify.LineRange{{From: 20, To: 20}}},
	}
	namesIPProtoRules = []classify.LineRule{
		{Ranges: []classify.LineRange{{From: 6, To: 6}}},
		{Ranges: []classify.LineRange{{From: 17, To: 17}}},
	}
	namesNet4SrcRules = []classify.Net4Rule{
		{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
		{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.2.0.0/16")}},
	}
	namesNet4DstRules = []classify.Net4Rule{
		{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}},
		{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}},
	}
	namesDeviceRules = []classify.DeviceRule{{IDs: []uint32{0}}, {IDs: []uint32{3}}}
	// The second source is a host inside the first one's network, so the
	// source attribute keeps its table.
	namesNet6SrcRules = []classify.Net6Rule{
		{Networks: []classify.Net6Network{net6Network(0x2a02_06b8_0000_0000, 64, 0, 0)}},
		{Networks: []classify.Net6Network{net6Network(0x2a02_06b8_0000_0000, 64, 0, 64)}},
	}
	namesNet6DstRules = []classify.Net6Rule{
		{Networks: []classify.Net6Network{net6Network(0xfe80_0000_0000_0000, 64, 0, 0)}},
		{Networks: []classify.Net6Network{net6Network(0xfe80_0000_0000_0000, 64, 0, 0)}},
	}
	namesFragmentRules = []classify.FragmentRule{{Fragment: classify.FragmentNone}, {Fragment: classify.FragmentFrag}}
	namesSrcPortRules  = []classify.PortRule{
		{Ranges: []classify.PortRange{{From: 1000, To: 2000}}},
		{Ranges: []classify.PortRange{{From: 1000, To: 1500}}},
	}
	namesDstPortRules = []classify.PortRule{
		{Ranges: []classify.PortRange{{From: 2000, To: 3000}}},
		{Ranges: []classify.PortRange{{From: 2500, To: 3000}}},
	}
)

// namesArtefacts are the artefacts of the composition beyond its lines
// and IPv4 networks, carved from its memory.
type namesArtefacts struct {
	Device   layout.ClassifyAttrDevice
	Nets4    layout.ValueTable
	Mid4     layout.ValueTable
	Proto4   layout.ValueTable
	Fragment layout.ClassifyAttrIpfrag
	Root4    layout.ValueTable
	RuleMap4 layout.Vline
	Net6Src  layout.ClassifyAttrNet6
	Net6Dst  layout.ClassifyAttrNet6
	Nets6    layout.ValueTable
	RuleMap6 layout.Vline
	SrcPort  layout.ClassifyAttrPort
	DstPort  layout.ClassifyAttrPort
	Ports    layout.ValueTable
}

// composeNames commits the compiles, joints and decodes of the two rules
// under one root context, with the family scoped entry names the modules
// pass, as context_names.c builds its composition, and returns the
// memory, the artefacts carved from it and the allocator's free bytes
// before the commits.
func composeNames(t *testing.T) (*classifytest.Memory, *namesArtefacts, uintptr) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	free := shmalloc.FreeSize(&memory.Allocator)
	artefacts := classifytest.Alloc[namesArtefacts](memory)

	devices, err := classify.CompileDevice(namesDeviceRules)
	require.NoError(t, err)
	require.NoError(t, devices.Commit(&artefacts.Device, &memory.Context, "l2:device"))
	source, err := classify.CompileNet4(namesNet4SrcRules)
	require.NoError(t, err)
	require.NoError(t, source.Commit(&memory.Net4, &memory.Context, "ip4:net4_src"))
	destination, err := classify.CompileNet4(namesNet4DstRules)
	require.NoError(t, err)
	require.NoError(t, destination.Commit(&memory.OtherNet4, &memory.Context, "ip4:net4_dst"))
	nets4, err := source.Stage().Join(destination.Stage(), &artefacts.Nets4, &memory.Context, "ip4:nets_joint")
	require.NoError(t, err)
	mid4, err := devices.Stage().Join(nets4, &artefacts.Mid4, &memory.Context, "ip4:mid_joint")
	require.NoError(t, err)
	protocol, err := classify.CompileLine(namesIPProtoRules, 0x100)
	require.NoError(t, err)
	require.NoError(t, protocol.Commit(&memory.Line, &memory.Context, "ip4:ipproto"))
	proto4, err := mid4.Join(protocol.Stage(), &artefacts.Proto4, &memory.Context, "ip4:proto_joint")
	require.NoError(t, err)
	fragment, err := classify.CompileFragment(namesFragmentRules)
	require.NoError(t, err)
	require.NoError(t, fragment.Commit(&artefacts.Fragment, &memory.Context, "ip4:ipfrag"))
	root4, err := proto4.Join(fragment.Stage(), &artefacts.Root4, &memory.Context, "ip4:root_joint")
	require.NoError(t, err)
	require.NoError(t, root4.Decode([]bool{true, true}, &artefacts.RuleMap4, &memory.Context, "ip4:rules"))

	source6, err := classify.CompileNet6(namesNet6SrcRules)
	require.NoError(t, err)
	require.NoError(t, source6.Commit(&artefacts.Net6Src, &memory.Context, "ip6:net6_src"))
	destination6, err := classify.CompileNet6(namesNet6DstRules)
	require.NoError(t, err)
	require.NoError(t, destination6.Commit(&artefacts.Net6Dst, &memory.Context, "ip6:net6_dst"))
	nets6, err := source6.Stage().Join(destination6.Stage(), &artefacts.Nets6, &memory.Context, "ip6:nets_joint")
	require.NoError(t, err)
	require.NoError(t, nets6.Decode([]bool{true, true}, &artefacts.RuleMap6, &memory.Context, "ip6:rules"))

	vlan, err := classify.CompileLine(namesVlanRules, 4096)
	require.NoError(t, err)
	require.NoError(t, vlan.Commit(&memory.OtherLine, &memory.Context, "l2:vlan"))
	srcPort := classify.CompilePort(namesSrcPortRules)
	require.NoError(t, srcPort.Commit(&artefacts.SrcPort, &memory.Context, "l4:src_port"))
	dstPort := classify.CompilePort(namesDstPortRules)
	require.NoError(t, dstPort.Commit(&artefacts.DstPort, &memory.Context, "l4:dst_port"))
	_, err = srcPort.Stage().Join(dstPort.Stage(), &artefacts.Ports, &memory.Context, "l4:ports_joint")
	require.NoError(t, err)
	return memory, artefacts, free
}

// rootChildren returns the names of the root context's children.
func rootChildren(memory *classifytest.Memory) []string {
	var names []string
	for child := memory.Context.FirstChild.Get(); child != nil; child = child.NextSibling.Get() {
		names = append(names, shm.String(child.Name[:]))
	}
	return names
}

// Test_LeafName_NoSiblingNameCollision verifies that no two sibling
// memory contexts, at any depth under the composition's root, share a
// name.
func Test_LeafName_NoSiblingNameCollision(t *testing.T) {
	memory, _, _ := composeNames(t)

	var walk func(context *layout.MemoryContext)
	walk = func(context *layout.MemoryContext) {
		seen := map[string]bool{}
		for child := context.FirstChild.Get(); child != nil; child = child.NextSibling.Get() {
			name := shm.String(child.Name[:])
			require.False(t, seen[name], "duplicate sibling %q under %q", name, shm.String(context.Name[:]))
			seen[name] = true
			walk(child)
		}
	}
	walk(&memory.Context)
}

// Test_LeafName_LongEntryNameCutsBeforeTheLeaf verifies that an entry
// name longer than a full title is cut with a dot marker before the
// leaf, so the leaf telling siblings apart survives and the cut stays
// visible in the memory tree.
func Test_LeafName_LongEntryNameCutsBeforeTheLeaf(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, memorySize))
	entry := strings.Repeat("x", 2*len(layout.MemoryContext{}.Name)-1)

	nets, err := classify.CompileNet4(nil)
	require.NoError(t, err)
	require.NoError(t, nets.Commit(&memory.Net4, &memory.Context, entry))
	line, err := classify.CompileLine(nil, 0x100)
	require.NoError(t, err)
	require.NoError(t, line.Commit(&memory.Line, &memory.Context, entry))

	// A cut title fills the 63 characters a name holds.
	require.Equal(t, strings.Repeat("x", 55)+"...:line", shm.String(memory.Line.Line.MemoryContext.Get().Name[:]))
	require.Equal(t, strings.Repeat("x", 56)+"...:lpm", shm.String(memory.Net4.Lpm.MemoryContext.Name[:]))
}

// Test_LeafName_AttributeChildrenAreLeafNamed verifies that every
// compile, join and decode leaves its artefacts behind as children of
// the root titled with its entry name and artefact leaf, a decode with
// its entry name alone.
//
// C also leaves a registry child per compile and join; the native ones
// keep their stages in the Go heap, so the artefacts are the root's
// only children. The destination networks split no row, so they leave
// no table.
func Test_LeafName_AttributeChildrenAreLeafNamed(t *testing.T) {
	memory, _, _ := composeNames(t)

	require.ElementsMatch(t, []string{
		"l2:device:line", "ip4:net4_src:lpm", "ip4:net4_dst:lpm", "ip4:nets_joint:table",
		"ip4:mid_joint:table", "ip4:ipproto:line", "ip4:proto_joint:table", "ip4:ipfrag:table",
		"ip4:root_joint:table", "ip4:rules", "ip6:net6_src:hi", "ip6:net6_src:lo",
		"ip6:net6_src:comb", "ip6:net6_dst:hi", "ip6:net6_dst:lo", "ip6:nets_joint:table",
		"ip6:rules", "l2:vlan:line", "l4:src_port:line", "l4:dst_port:line", "l4:ports_joint:table",
	}, rootChildren(memory))
}

// Test_LeafName_JointChildrenAreLeafNamed verifies that a joint leaves
// exactly one table child behind, leaf named after the joint.
func Test_LeafName_JointChildrenAreLeafNamed(t *testing.T) {
	memory, _, _ := composeNames(t)

	for _, joint := range []string{"ip4:nets_joint", "ip4:mid_joint", "ip4:proto_joint", "ip4:root_joint", "ip6:nets_joint", "l4:ports_joint"} {
		require.Equal(t, 1, countOf(rootChildren(memory), joint+":table"), "the table of %s", joint)
	}
}

// Test_LeafName_DecodeRuleMapKeepsEntryName verifies that a decode
// titles its rule map with the plain entry name: it leaves a single
// context behind, so no leaf tells siblings apart.
func Test_LeafName_DecodeRuleMapKeepsEntryName(t *testing.T) {
	memory, _, _ := composeNames(t)

	require.Equal(t, 1, countOf(rootChildren(memory), "ip4:rules"))
	require.Equal(t, 1, countOf(rootChildren(memory), "ip6:rules"))
}

// countOf returns how many times a name occurs.
func countOf(names []string, name string) int {
	count := 0
	for _, each := range names {
		if each == name {
			count++
		}
	}
	return count
}

// Test_LeafName_TeardownLeavesNothingBehind verifies that freeing the
// composition leaves the root context childless and gives the allocator
// back every byte the commits took.
func Test_LeafName_TeardownLeavesNothingBehind(t *testing.T) {
	memory, artefacts, free := composeNames(t)

	classify.FreeJoin(&artefacts.Ports)
	classify.FreePort(&artefacts.SrcPort)
	classify.FreePort(&artefacts.DstPort)
	classify.FreeDecode(&artefacts.RuleMap6)
	classify.FreeJoin(&artefacts.Nets6)
	classify.FreeNet6(&artefacts.Net6Dst)
	classify.FreeNet6(&artefacts.Net6Src)
	classify.FreeDecode(&artefacts.RuleMap4)
	classify.FreeJoin(&artefacts.Root4)
	classify.FreeFragment(&artefacts.Fragment)
	classify.FreeJoin(&artefacts.Proto4)
	classify.FreeJoin(&artefacts.Mid4)
	classify.FreeJoin(&artefacts.Nets4)
	classify.FreeDevice(&artefacts.Device)
	shmalloc.ContextFree(&memory.Context, unsafe.Pointer(artefacts), unsafe.Sizeof(*artefacts))
	classify.FreeLine(&memory.OtherLine)
	classify.FreeLine(&memory.Line)
	classify.FreeNet4(&memory.OtherNet4)
	classify.FreeNet4(&memory.Net4)

	require.True(t, memory.Context.FirstChild.IsNil(), "the root context has no children")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}
