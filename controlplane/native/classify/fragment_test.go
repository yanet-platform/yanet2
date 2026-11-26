//go:build linux && (amd64 || arm64)

package classify_test

import (
	"net/netip"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// fragmentMemorySize is the size of a memory a fragment attribute fits.
const fragmentMemorySize = 1 << 20

// fragmentRule draws a rule of any fragment constraint or an absent one.
var fragmentRule = rapid.Custom(func(t *rapid.T) classify.FragmentRule {
	return classify.FragmentRule{
		Fragment: rapid.SampledFrom([]classify.Fragment{classify.FragmentAny, classify.FragmentNone, classify.FragmentFrag}).Draw(t, "fragment"),
		Absent:   rapid.IntRange(0, 5).Draw(t, "absent") == 0,
	}
})

// fragmentClass returns the class a committed fragment attribute gives
// a fragment or a whole packet.
func fragmentClass(attr *layout.ClassifyAttrIpfrag, fragment bool) uint32 {
	region := 0
	if fragment {
		region = 1
	}
	return classifytest.Table(&attr.ValueTable)[0][region]
}

// Test_CompileFragment_MatchesReference verifies that fragments and
// whole packets get classes whose stage reaches exactly the rules
// matching them.
func Test_CompileFragment_MatchesReference(t *testing.T) {
	rapid.Check(t, fragmentProperty)
}

// FuzzCompileFragment verifies the reference property on rulesets the
// fuzzer draws.
func FuzzCompileFragment(f *testing.F) {
	f.Fuzz(rapid.MakeFuzz(fragmentProperty))
}

// fragmentProperty is the property that any fragment ruleset compiles to
// classes whose stage reaches exactly the rules matching fragments and
// whole packets.
func fragmentProperty(t *rapid.T) {
	rules := rapid.SliceOfN(fragmentRule, 0, 16).Draw(t, "rules")
	memory := classifytest.LayOut(make([]byte, fragmentMemorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrIpfrag](memory)
	fragments, err := classify.CompileFragment(rules)
	require.NoError(t, err)
	require.NoError(t, fragments.Commit(attr, &memory.Context, "ipfrag"))

	err = classifytest.CheckStage(
		fragments.Stage(),
		[]bool{false, true},
		func(fragment bool) uint32 { return fragmentClass(attr, fragment) },
		func(rule int, fragment bool) bool { return classifytest.FragmentMatches(rules[rule], fragment) },
	)
	require.NoError(t, err)
}

// Test_CompileFragment_RefusesUnknownConstraint verifies that a fragment
// constraint outside the enumeration is refused as an invalid rule.
func Test_CompileFragment_RefusesUnknownConstraint(t *testing.T) {
	_, err := classify.CompileFragment([]classify.FragmentRule{{Fragment: classify.FragmentFrag}, {Fragment: 3}})
	require.ErrorIs(t, err, classify.ErrInvalidRule)
}

// Test_FragmentCommit_ExhaustedLeavesAttributeZeroed verifies that a
// fragment commit refused any of its allocations fails as exhausted,
// leaves the attribute zeroed and gives the allocator back every byte it
// took.
func Test_FragmentCommit_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	context := unsafe.Sizeof(layout.MemoryContext{})
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the row array", kept: []uintptr{context}},
		{name: "no room for the row", kept: []uintptr{context, shm.PtrSize}},
	}
	fragments, err := classify.CompileFragment([]classify.FragmentRule{{Fragment: classify.FragmentFrag}, {}})
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, fragmentMemorySize))
			attr := classifytest.Alloc[layout.ClassifyAttrIpfrag](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			require.ErrorIs(t, fragments.Commit(attr, &memory.Context, "ipfrag"), shmalloc.ErrNoMemory)
			requireZeroed(t, attr, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the table's memory context is unlinked")
		})
	}
}

// Test_FreeFragment_ReturnsEveryByte verifies that freeing a committed
// fragment attribute zeroes it and gives the allocator back every byte
// the commit took.
func Test_FreeFragment_ReturnsEveryByte(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, fragmentMemorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrIpfrag](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	fragments, err := classify.CompileFragment([]classify.FragmentRule{{Fragment: classify.FragmentNone}})
	require.NoError(t, err)
	require.NoError(t, fragments.Commit(attr, &memory.Context, "ipfrag"))

	classify.FreeFragment(attr)
	requireZeroed(t, attr, "the attribute")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.True(t, memory.Context.FirstChild.IsNil(), "the table's memory context is unlinked")
}

// plainFilter is what the fragment scenarios commit: the core of any
// device and any networks with the protocol line, the fragment attribute,
// the joints chaining them and the rule map of the root.
type plainFilter struct {
	Device      layout.ClassifyAttrDevice
	Source      layout.ClassifyAttrNet4
	Destination layout.ClassifyAttrNet4
	Protocol    layout.ClassifyAttrLine
	Fragment    layout.ClassifyAttrIpfrag
	Nets        layout.ValueTable
	Mid         layout.ValueTable
	Core        layout.ValueTable
	Root        layout.ValueTable
	RuleMap     layout.Vline
}

// commitPlainFilter compiles the protocol intervals and the fragment
// constraints of rules over any device and any networks, as the ACL
// module's plain filter joins them, and returns the rule a packet of a
// protocol, a fragment or not, resolves to.
func commitPlainFilter(t *testing.T, protocols []classify.LineRange, fragments []classify.Fragment) func(protocol uint32, fragment bool) uint32 {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	filter := classifytest.Alloc[plainFilter](memory)
	context := &memory.Context
	any4 := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	devices := make([]classify.DeviceRule, len(protocols))
	nets := make([]classify.Net4Rule, len(protocols))
	protocolRules := make([]classify.LineRule, len(protocols))
	fragmentRules := make([]classify.FragmentRule, len(protocols))
	members := make([]bool, len(protocols))
	for idx := range protocols {
		nets[idx] = classify.Net4Rule{Prefixes: any4}
		protocolRules[idx] = classify.LineRule{Ranges: protocols[idx : idx+1]}
		fragmentRules[idx] = classify.FragmentRule{Fragment: fragments[idx]}
		members[idx] = true
	}

	device, err := classify.CompileDevice(devices)
	require.NoError(t, err)
	require.NoError(t, device.Commit(&filter.Device, context, "fragment_device"))
	source, err := classify.CompileNet4(nets)
	require.NoError(t, err)
	require.NoError(t, source.Commit(&filter.Source, context, "fragment_net4_src"))
	destination, err := classify.CompileNet4(nets)
	require.NoError(t, err)
	require.NoError(t, destination.Commit(&filter.Destination, context, "fragment_net4_dst"))
	netsStage, err := source.Stage().Join(destination.Stage(), &filter.Nets, context, "nets_joint")
	require.NoError(t, err)
	mid, err := device.Stage().Join(netsStage, &filter.Mid, context, "mid_joint")
	require.NoError(t, err)
	protocol, err := classify.CompileLine(protocolRules, 0x100)
	require.NoError(t, err)
	require.NoError(t, protocol.Commit(&filter.Protocol, context, "fragment_ipproto"))
	core, err := mid.Join(protocol.Stage(), &filter.Core, context, "proto_joint")
	require.NoError(t, err)
	fragment, err := classify.CompileFragment(fragmentRules)
	require.NoError(t, err)
	require.NoError(t, fragment.Commit(&filter.Fragment, context, "fragment_ipfrag"))
	root, err := core.Join(fragment.Stage(), &filter.Root, context, "root_joint")
	require.NoError(t, err)
	require.NoError(t, root.Decode(members, &filter.RuleMap, context, "rule_map"))

	midClass := classifytest.Table(&filter.Mid)[0][0]
	protocolClasses := classifytest.Values(&filter.Protocol.Line)
	coreTable, rootTable := classifytest.Table(&filter.Core), classifytest.Table(&filter.Root)
	ruleMap := classifytest.Values(&filter.RuleMap)
	return func(protocol uint32, fragment bool) uint32 {
		coreClass := coreTable[midClass][protocolClasses[protocol]]
		return ruleMap[rootTable[coreClass][fragmentClass(&filter.Fragment, fragment)]]
	}
}

// Test_CompileFragment_PlainFilterScenarios verifies that a pair of
// protocol rules resolves fragments and whole packets through the rule
// their constraints let match first.
//
// It translates the scenarios of the C library's fragment test: a
// protocol-zero deny rule before an any-protocol allow rule, and a deny
// rule for TCP fragments before the same allow rule.
func Test_CompileFragment_PlainFilterScenarios(t *testing.T) {
	type probe struct {
		protocol uint32
		fragment bool
		rule     uint32
	}
	allow := classify.LineRange{From: 0, To: 255}
	cases := []struct {
		name      string
		protocols []classify.LineRange
		fragments []classify.Fragment
		probes    []probe
	}{
		{
			name:      "protocol-zero deny before any-protocol allow",
			protocols: []classify.LineRange{{From: 0, To: 0}, allow},
			fragments: []classify.Fragment{classify.FragmentAny, classify.FragmentAny},
			probes:    []probe{{protocol: 0, fragment: false, rule: 0}, {protocol: 6, fragment: true, rule: 1}},
		},
		{
			name:      "TCP fragment deny before any-protocol allow",
			protocols: []classify.LineRange{{From: 6, To: 6}, allow},
			fragments: []classify.Fragment{classify.FragmentFrag, classify.FragmentAny},
			probes:    []probe{{protocol: 6, fragment: true, rule: 0}, {protocol: 17, fragment: true, rule: 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ruleOf := commitPlainFilter(t, tc.protocols, tc.fragments)
			for _, packet := range tc.probes {
				require.Equal(t, packet.rule, ruleOf(packet.protocol, packet.fragment), "the rule of %+v", packet)
			}
		})
	}
}
