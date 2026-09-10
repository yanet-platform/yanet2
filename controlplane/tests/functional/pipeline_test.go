// Package controlplane_test pins the error kinds the pipeline, function and
// device mutations report through the shared-memory agent.
//
// References resolve only when the graph is live: nothing checks a pipeline
// or function no device runs, so a dangling name there is legal, while the
// same name in a live graph is a failed precondition and a delete of an
// entity the live graph still runs fails the same way.
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
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// Memory sizes are generous because every graph compiles one ACL module in
// the agent arena.
const (
	cpMemory    = 256 * datasize.MB
	dpMemory    = 16 * datasize.MB
	agentMemory = 128 * datasize.MB
)

// newAgent starts a single-worker harness with the acl module and the plain
// device loaded and attaches an agent to it.
func newAgent(t *testing.T, name string) *ffi.Agent {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(cpMemory),
		DPMemory:      uint64(dpMemory),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach(name, 0, agentMemory)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent
}

// publishACL publishes an ACL module that allows every IPv4 UDP packet, the
// one module the graphs under test name.
func publishACL(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	backend := acl.NewBackend(agent)
	rule := cacl.AclRule{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
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
	handle, err := backend.NewModule(name, []cacl.AclRule{rule}, "", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))
}

// singleChainFunction returns a single-chain function config running the
// named acl module.
func singleChainFunction(function, module string) ffi.FunctionConfig {
	return ffi.FunctionConfig{
		Name: function,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    function + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: module}},
			},
		}},
	}
}

// publishGraph publishes acl module, function and pipeline all called name,
// each running the previous one, without attaching the pipeline anywhere.
func publishGraph(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	publishACL(t, agent, name)
	require.NoError(t, agent.UpdateFunction(singleChainFunction(name, name)))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      name,
		Functions: []string{name},
	}))
}

// attach publishes port0 running the named pipeline on its input side and
// an empty pipeline on its output side, which makes the named graph live.
func attach(t *testing.T, agent *ffi.Agent, input string) {
	t.Helper()

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "egress"}))
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: input, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "egress", Weight: 1}},
	}})
	require.NoError(t, err)
}

// detach moves port0 input to a second empty pipeline, so no device runs
// the graph attached before.
//
// A device cannot run one pipeline on both of its sides, so the output
// pipeline cannot take that role.
func detach(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "idle"}))
	attach(t, agent, "idle")
}

// Test_DeletePipeline_Missing_NotFound verifies that deleting a pipeline the
// configuration does not hold reports the not-found kind.
func Test_DeletePipeline_Missing_NotFound(t *testing.T) {
	agent := newAgent(t, "pipeline-missing")

	err := agent.DeletePipeline("ghost")
	require.ErrorIs(t, err, ffi.ErrNotFound)
}

// Test_DeleteFunction_Missing_NotFound verifies that deleting a function the
// configuration does not hold reports the not-found kind.
func Test_DeleteFunction_Missing_NotFound(t *testing.T) {
	agent := newAgent(t, "function-missing")

	err := agent.DeleteFunction("ghost")
	require.ErrorIs(t, err, ffi.ErrNotFound)
}

// Test_DeletePipeline_Live_FailedPrecondition verifies that deleting a
// pipeline a device runs reports the failed-precondition kind.
//
// The pipeline stays in place, so detaching it makes the same delete
// succeed.
func Test_DeletePipeline_Live_FailedPrecondition(t *testing.T) {
	agent := newAgent(t, "pipeline-live")
	publishGraph(t, agent, "live")
	attach(t, agent, "live")

	err := agent.DeletePipeline("live")
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	detach(t, agent)
	require.NoError(t, agent.DeletePipeline("live"))
}

// Test_DeleteFunction_Live_FailedPrecondition verifies that deleting a
// function a live pipeline runs reports the failed-precondition kind.
//
// The same delete succeeds once no device runs that pipeline, even though
// it still names the function.
func Test_DeleteFunction_Live_FailedPrecondition(t *testing.T) {
	agent := newAgent(t, "function-live")
	publishGraph(t, agent, "live")
	attach(t, agent, "live")

	err := agent.DeleteFunction("live")
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	detach(t, agent)
	require.NoError(t, agent.DeleteFunction("live"))
}

// Test_UpdatePipeline_Live_MissingFunction_FailedPrecondition verifies that
// a live pipeline cannot start naming a function that does not exist.
//
// An unattached pipeline can.
func Test_UpdatePipeline_Live_MissingFunction_FailedPrecondition(t *testing.T) {
	agent := newAgent(t, "pipeline-dangling")
	publishGraph(t, agent, "live")
	attach(t, agent, "live")

	dangling := ffi.PipelineConfig{Name: "live", Functions: []string{"ghost"}}
	err := agent.UpdatePipeline(dangling)
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	detach(t, agent)
	require.NoError(t, agent.UpdatePipeline(dangling))
}

// Test_UpdateFunction_Live_MissingModule_FailedPrecondition verifies that a
// function a live pipeline runs cannot start naming a missing module.
//
// It can once no device runs that pipeline.
func Test_UpdateFunction_Live_MissingModule_FailedPrecondition(t *testing.T) {
	agent := newAgent(t, "function-dangling")
	publishGraph(t, agent, "live")
	attach(t, agent, "live")

	dangling := singleChainFunction("live", "ghost")
	err := agent.UpdateFunction(dangling)
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	detach(t, agent)
	require.NoError(t, agent.UpdateFunction(dangling))
}

// Test_UpdateDevice_MissingPipeline_FailedPrecondition verifies that a device
// cannot start running a pipeline that does not exist.
func Test_UpdateDevice_MissingPipeline_FailedPrecondition(t *testing.T) {
	agent := newAgent(t, "device-dangling")

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "egress"}))
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "ghost", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "egress", Weight: 1}},
	}})
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)
}
