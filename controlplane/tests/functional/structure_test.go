package controlplane_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// The provenance scenarios keep two agent arenas alive at once — the
// earlier agent's arena survives its close while the generation still
// references its module — so the control-plane memory must hold both.
const structureCPMemory = 512 * datasize.MB

// newStructureHarness starts a single-worker harness with the acl module
// and the plain device loaded, ready for several agents to publish into
// one shared memory: the structure reader attributes modules per
// publishing agent, so the scenarios attach a fresh agent per step.
func newStructureHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(structureCPMemory),
		DPMemory:      uint64(dpMemory),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	return h
}

// publishACLModule publishes one acl module allowing every IPv4 UDP
// packet, without registering any test cleanup.
//
// The provenance scenario closes an agent mid-test, so module handles
// must not outlive the mapping they point into.
func publishACLModule(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	rule := cacl.ACLRule{
		Actions:       []cacl.ACLAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Src6s:         []xnetip.BiContiguous{},
		Dst6s:         []xnetip.BiContiguous{},
		SrcPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		DstPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.AnySubtype()),
		},
		Fragment: filter.FragmentAny,
	}

	backend := acl.NewBackend(agent)
	handle, err := backend.NewModule(name, []cacl.ACLRule{rule}, "", "")
	require.NoError(t, err)
	require.NoError(t, backend.UpdateModule(handle))
}

// Test_CPStructure_PublishedGraph_ReflectsWholeStructure verifies that one
// snapshot reports the module with its publishing agent, the function with
// its chain, the pipeline naming that function and the device bound to the
// pipeline, all together.
func Test_CPStructure_PublishedGraph_ReflectsWholeStructure(t *testing.T) {
	h := newStructureHarness(t)
	agent, err := h.SharedMemory().AgentAttach(
		"structure-publisher", 0, agentMemory,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	publishGraph(t, agent, "graph")
	attach(t, agent, "graph")

	structure := agent.DPConfig().CPStructure()

	require.Len(t, structure.Modules, 1)
	require.Equal(t, "acl", structure.Modules[0].Type)
	require.Equal(t, "graph", structure.Modules[0].Name)
	require.Equal(t, "structure-publisher", structure.Modules[0].Agent)

	require.Len(t, structure.Functions, 1)
	require.Equal(t, "graph", structure.Functions[0].Name)
	require.Len(t, structure.Functions[0].Chains, 1)
	require.Equal(t, "graph_chain", structure.Functions[0].Chains[0].Name)
	require.Equal(t, uint64(1), structure.Functions[0].Chains[0].Weight)
	require.Len(t, structure.Functions[0].Chains[0].Modules, 1)
	require.Equal(t, "graph", structure.Functions[0].Chains[0].Modules[0].Name)

	pipelines := make(map[string][]string, len(structure.Pipelines))
	for _, pipeline := range structure.Pipelines {
		pipelines[pipeline.Name] = pipeline.Functions
	}
	require.Equal(t, []string{"graph"}, pipelines["graph"])

	require.Len(t, structure.Devices, 1)
	require.Equal(t, "port0", structure.Devices[0].Name)
	require.Len(t, structure.Devices[0].InputPipelines, 1)
	require.Equal(t, "graph", structure.Devices[0].InputPipelines[0].Name)
	require.Equal(t, uint64(1), structure.Devices[0].InputPipelines[0].Weight)
}

// Test_CPStructure_ModuleProvenance_FollowsPublishingAgent verifies that a
// module keeps the name of the agent that published it after that agent is
// gone, and that a replacement published by a later agent carries the
// later name instead.
func Test_CPStructure_ModuleProvenance_FollowsPublishingAgent(t *testing.T) {
	h := newStructureHarness(t)

	earlier, err := h.SharedMemory().AgentAttach(
		"structure-earlier", 0, agentMemory,
	)
	require.NoError(t, err)
	publishACLModule(t, earlier, "survivor")
	require.NoError(t, earlier.Close())

	surviving := h.SharedMemory().DPConfig(0).CPStructure()
	require.Len(t, surviving.Modules, 1)
	require.Equal(t, "survivor", surviving.Modules[0].Name)
	require.Equal(t, "structure-earlier", surviving.Modules[0].Agent)

	later, err := h.SharedMemory().AgentAttach(
		"structure-later", 0, agentMemory,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = later.CleanUp() })
	publishACLModule(t, later, "survivor")

	replaced := h.SharedMemory().DPConfig(0).CPStructure()
	require.Len(t, replaced.Modules, 1)
	require.Equal(t, "survivor", replaced.Modules[0].Name)
	require.Equal(t, "structure-later", replaced.Modules[0].Agent)
}
