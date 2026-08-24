package acl_test

import (
	"github.com/yanet-platform/xnetip"
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

	backend := acl.NewBackend(agent)
	return agent, backend
}

// TestACL_FWStateAgentSharing_TypedDestroyIsolatedPerModule reproduces
// production agent sharing on the dangling-free protocol.
//
// The acl module attaches one agent and hands it to both the ACL and
// fwstate services. The sequence mirrors the fwstate service's own
// release path: the owner's free is refused while the published
// generation still references the module, and the delete that retires
// that generation lets the pending free destroy it through fwstate's own
// typed destructor. An ACL update on the shared agent must neither
// destroy nor corrupt the fwstate module: destruction is scoped to the
// owner that knows the type, never to whatever else shares the arena.
func TestACL_FWStateAgentSharing_TypedDestroyIsolatedPerModule(t *testing.T) {
	agent, backend := setupACLFWStateHarness(t)

	fwCfg, err := cfwstate.NewModuleConfig(agent, "fw0", nil, "", "")
	require.NoError(t, err)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwCfg.AsFFIModule()}))

	// The owner's free attempt is refused while the published generation
	// still references fw0, and is queued for a retry.
	fwCfg.Free()

	// Retiring the generation that still references fw0 is what lets the
	// queued free succeed: the delete retries it on its way out and
	// fwstate's own destructor destroys the module.
	require.NoError(t, agent.DeleteModuleConfig("fwstate", "fw0"))

	// An ACL update on the shared agent must not touch anything but its
	// own acl modules: destruction runs only through each owner's own
	// free path.
	rules := []cacl.AclRule{
		allow4Rule(
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			udpProto,
		),
	}
	handle := applyACLRules(t, backend, "acl0", rules)
	require.NotNil(t, handle)
}
