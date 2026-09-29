package pipeline_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
)

// workerSweepIntervalNs mirrors the pipeline round's periodic sweep
// interval.
//
// The value is a behavioural contract, not an incidental constant: it is
// what keeps an idle worker's round cost independent of the device count,
// so a change here must update the C constant in the same change.
const workerSweepIntervalNs = uint64(1000)

// setupSweepHarness builds a one-device harness whose published generation
// carries the device's entries on the worker's home list.
//
// No module is needed: the empty round under test runs the entries through
// an empty pipeline, which is all the sweep gate observes. The wiring ends
// with the device update so the worker holds the final generation.
func setupSweepHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(dispatchCPSize),
		DPMemory:      uint64(dispatchDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("sweep-test", 0, dispatchMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	return h
}

// Test_WorkerPipelineRound_SweepDeadlineGate verifies that an empty round
// sweeps the untouched device entries only once the sweep deadline has
// expired.
//
// A fresh generation starts with an expired deadline, so the first round
// always sweeps; a round before the armed deadline skips the sweep and
// leaves the deadline in place; a round exactly at the deadline sweeps
// again, so the boundary is inclusive.
func Test_WorkerPipelineRound_SweepDeadlineGate(t *testing.T) {
	h := setupSweepHarness(t)

	base := time.Unix(0, 1_000_000)
	h.SetCurrentTime(base)

	// First round on the fresh generation: the sweep fires and arms the
	// deadline one interval past the round's time.
	_, err := h.HandlePackets()
	require.NoError(t, err)
	deadline, err := h.WorkerSweepDeadline(0)
	require.NoError(t, err)
	require.Equal(
		t,
		uint64(base.UnixNano())+workerSweepIntervalNs,
		deadline,
		"the first round on a fresh generation must sweep and arm the deadline one interval out",
	)

	// Half an interval before the deadline: the sweep is skipped and the
	// deadline stays armed where it was.
	h.SetCurrentTime(base.Add(time.Duration(workerSweepIntervalNs / 2)))
	_, err = h.HandlePackets()
	require.NoError(t, err)
	deadline, err = h.WorkerSweepDeadline(0)
	require.NoError(t, err)
	require.Equal(
		t,
		uint64(base.UnixNano())+workerSweepIntervalNs,
		deadline,
		"a round before the deadline must leave the deadline in place",
	)

	// Exactly at the deadline: the gate opens, so the sweep runs and arms
	// the next deadline one interval past this round's time.
	h.SetCurrentTime(base.Add(time.Duration(workerSweepIntervalNs)))
	_, err = h.HandlePackets()
	require.NoError(t, err)
	deadline, err = h.WorkerSweepDeadline(0)
	require.NoError(t, err)
	require.Equal(
		t,
		uint64(base.UnixNano())+2*workerSweepIntervalNs,
		deadline,
		"a round exactly at the deadline must sweep again (boundary is inclusive)",
	)
}
