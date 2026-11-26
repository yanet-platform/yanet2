//go:build linux && (amd64 || arm64)

package classify_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// The line and IPv4 network views of the two rules lib/classify's
// context_names.c composes.
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
)

// composeNames commits the line and IPv4 network compiles of the two
// rules under one root context, with the family scoped entry names the
// modules pass, as context_names.c builds its composition, and returns
// the memory and the allocator's free bytes before the commits.
func composeNames(t *testing.T) (*classifytest.Memory, uintptr) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	free := shmalloc.FreeSize(&memory.Allocator)

	source, err := classify.CompileNet4(namesNet4SrcRules)
	require.NoError(t, err)
	require.NoError(t, source.Commit(&memory.Net4, &memory.Context, "ip4:net4_src"))
	destination, err := classify.CompileNet4(namesNet4DstRules)
	require.NoError(t, err)
	require.NoError(t, destination.Commit(&memory.OtherNet4, &memory.Context, "ip4:net4_dst"))
	protocol, err := classify.CompileLine(namesIPProtoRules, 0x100)
	require.NoError(t, err)
	require.NoError(t, protocol.Commit(&memory.Line, &memory.Context, "ip4:ipproto"))
	vlan, err := classify.CompileLine(namesVlanRules, 4096)
	require.NoError(t, err)
	require.NoError(t, vlan.Commit(&memory.OtherLine, &memory.Context, "l2:vlan"))
	return memory, free
}

// Test_LeafName_NoSiblingNameCollision verifies that no two sibling
// memory contexts, at any depth under the composition's root, share a
// name.
func Test_LeafName_NoSiblingNameCollision(t *testing.T) {
	memory, _ := composeNames(t)

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

// Test_LeafName_AttributeChildrenAreLeafNamed verifies that every line
// and IPv4 network compile leaves its artefact behind as the one child
// of the root titled with its entry name and artefact leaf.
//
// C also leaves a registry child per compile; the native compile keeps
// its stage in the Go heap, so the artefacts are the root's only
// children.
func Test_LeafName_AttributeChildrenAreLeafNamed(t *testing.T) {
	memory, _ := composeNames(t)

	var names []string
	for child := memory.Context.FirstChild.Get(); child != nil; child = child.NextSibling.Get() {
		names = append(names, shm.String(child.Name[:]))
	}
	require.ElementsMatch(t, []string{"l2:vlan:line", "ip4:net4_src:lpm", "ip4:net4_dst:lpm", "ip4:ipproto:line"}, names)
}

// Test_LeafName_TeardownLeavesNothingBehind verifies that freeing the
// composition leaves the root context childless and gives the allocator
// back every byte the commits took.
func Test_LeafName_TeardownLeavesNothingBehind(t *testing.T) {
	memory, free := composeNames(t)

	classify.FreeLine(&memory.OtherLine)
	classify.FreeLine(&memory.Line)
	classify.FreeNet4(&memory.OtherNet4)
	classify.FreeNet4(&memory.Net4)

	require.True(t, memory.Context.FirstChild.IsNil(), "the root context has no children")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}
