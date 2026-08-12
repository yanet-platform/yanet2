package acl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// setupACLFWStateHarness builds a harness with acl, forward, and fwstate
// loaded and attaches one agent to all three, mirroring how the acl module
// wires its own agent to the fwstate service in production.
func setupACLFWStateHarness(tb testing.TB) (*ffi.Agent, acl.Backend) {
	tb.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(aclCPSize),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl", "forward", "fwstate"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(tb, err)
	tb.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("acl", 0, aclMemSize)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent, uint64(aclMemSize))
	return agent, backend
}

// TestACL_FWStateAgentSharing_ParkedModuleSurvivesUnrelatedDrain reproduces
// production agent sharing where fwstate parks during an ACL drain.
//
// The acl module attaches one agent and hands it to both the ACL and
// fwstate services. The sequence mirrors the fwstate service's own release
// path, followed by a delete that retires the generation still pinning it,
// parking the module before the ACL update runs. A drain call must stay
// scoped to its own module type: destroying the parked fwstate module with
// the wrong destructor would corrupt the arena during the filter
// compiler's teardown.
func TestACL_FWStateAgentSharing_ParkedModuleSurvivesUnrelatedDrain(t *testing.T) {
	agent, backend := setupACLFWStateHarness(t)

	fwCfg, err := cfwstate.NewModuleConfig(agent, "fw0")
	require.NoError(t, err)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwCfg.AsFFIModule()}))

	// Release the creator's reference while the published generation
	// still holds fw0: it must not park yet.
	fwCfg.Free()

	// Retiring the generation that still references fw0 is what actually
	// parks it.
	require.NoError(t, agent.DeleteModuleConfig("fwstate", "fw0"))

	// An ACL update on the shared agent must not touch the parked fwstate
	// module: its own drain call is filtered to the acl type.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}
	handle := applyACLRules(t, backend, "acl0", rules)
	require.NotNil(t, handle)
}
