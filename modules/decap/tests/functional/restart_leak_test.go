package decap_test

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
)

// TestRestart_WithoutRelease_ReclaimsSupersededAgents verifies that
// republishing without releasing never leaks an agent arena per restart.
//
// Construction also takes a reference for the module's creator, and a
// crashed creator never drops it. An earlier design mirrored that reference
// into the agent's live-module count, so it never returned to zero and the
// agent reclaimer withheld cleanup forever. Each round here republishes the
// same module name and abandons the returned handle without releasing it,
// then requires the previous generation to already be gone.
func TestRestart_WithoutRelease_ReclaimsSupersededAgents(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 16),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	const agentName = "decap-restart"
	const moduleName = "decap0"

	var baselineFreeBytes uint64
	for round := range 6 {
		agent, err := shm.AgentAttach(agentName, 0, datasize.MB*1)
		require.NoErrorf(t, err, "round %d: attach failed", round)

		// The returned handle is deliberately dropped rather than released,
		// modeling a creator that crashed before it could drop its own
		// reference.
		_, err = decap.NewBackend(agent).UpdateModule(
			moduleName,
			[]netip.Prefix{netip.MustParsePrefix("2001:db8::/32")},
		)
		require.NoErrorf(t, err, "round %d: publish failed", round)

		instances := restartTestAgentInstances(t, shm, agentName)
		require.Lenf(
			t, instances, 1,
			"round %d: %d superseded generation(s) were not reclaimed",
			round, len(instances)-1,
		)

		if round == 0 {
			baselineFreeBytes = instances[0].FreeBytes
		} else {
			require.Equalf(
				t, baselineFreeBytes, instances[0].FreeBytes,
				"round %d: free bytes drifted from the round-0 baseline",
				round,
			)
		}
	}
}

// restartTestAgentInstances returns the live generation list reported for
// the named agent: one entry per not-yet-reclaimed superseded generation.
func restartTestAgentInstances(
	t *testing.T, shm *ffi.SharedMemory, name string,
) []ffi.AgentInstanceInfo {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
		if agentInfo.Name == name {
			return agentInfo.Instances
		}
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return nil
}
