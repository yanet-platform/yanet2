package builtin_test

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
)

// Test_Function_UpdateAndDelete_Concurrent verifies that overlapping update
// and delete calls all succeed and leave the registry empty.
//
// Every call attaches an agent under one fixed name, and an attach reclaims
// the shared memory of the agent it supersedes, so calls that are not
// serialized would release an arena another call is still using.
func Test_Function_UpdateAndDelete_Concurrent(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	svc := builtin.NewFunction(0, harness.SharedMemory())

	const callers = 32
	const rounds = 64

	// Callers open their windows together and then repeat the round, so an
	// agent lifetime that is not serialized overlaps another one within a
	// single run.
	gate := make(chan struct{})

	group, ctx := errgroup.WithContext(t.Context())
	for caller := range callers {
		group.Go(func() error {
			id := &commonpb.FunctionId{Name: fmt.Sprintf("f%d", caller)}

			<-gate

			for range rounds {
				_, err := svc.Update(ctx, &ynpb.UpdateFunctionRequest{
					Function: &ynpb.Function{Id: id},
				})
				if err != nil {
					return err
				}
				if _, err := svc.Delete(ctx, &ynpb.DeleteFunctionRequest{Id: id}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	close(gate)
	require.NoError(t, group.Wait())

	response, err := svc.List(t.Context(), &ynpb.ListFunctionsRequest{})
	require.NoError(t, err)
	require.Empty(t, response.GetIds())
}

// Test_Function_Update_LeavesArenaSpare verifies that the agent an update
// leaves behind still reports spare room in its arena.
//
// Occupancy is reported as the reserved size minus what the allocator can
// still hand back, so an arena that yields no block at all reads as
// permanently full and puts this service at the top of the arena pressure
// readout while no call allocates from it at all.
func Test_Function_Update_LeavesArenaSpare(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	svc := builtin.NewFunction(0, shm)

	_, err = svc.Update(t.Context(), &ynpb.UpdateFunctionRequest{
		Function: &ynpb.Function{Id: &commonpb.FunctionId{Name: "spare"}},
	})
	require.NoError(t, err)

	var attached bool
	for _, agent := range shm.DPConfig(0).Agents() {
		if agent.Name != "function" {
			continue
		}
		attached = true
		for _, instance := range agent.Instances {
			require.NotZero(t, instance.FreeBytes, "arena reports no spare room")
		}
	}
	require.True(t, attached, "update left no attached agent behind")
}

// Test_Function_DirectUpdate_RejectsOverBoundWeight verifies that a chain
// weight above the documented bound and a chain weight sum above the bound
// are both rejected when an update reaches the config layer through the
// direct FFI path, which bypasses proto validation.
//
// The single-weight case is refused by the config setter, while the sum
// case is refused only by execution-context sizing, so the sum case wires
// the function into a device through a pipeline to reach the sizing path.
func Test_Function_DirectUpdate_RejectsOverBoundWeight(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("function-weight", 0, datasize.MB*4)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "f",
		Chains: []ffi.FunctionChainConfig{
			{Chain: ffi.ChainConfig{Name: "c0"}, Weight: 1},
		},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "p0",
		Functions: []string{"f"},
	}))

	device, err := cplain.NewDeviceConfig(
		agent,
		"d0",
		[]cplain.Pipeline{{Name: "p0", Weight: 1}},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = device.Free() })
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{device.AsFFIDevice()}))

	for _, tc := range []struct {
		name     string
		function string
		chains   []ffi.FunctionChainConfig
		message  string
	}{
		{
			name:     "weight above the bound",
			function: "oversized",
			chains: []ffi.FunctionChainConfig{
				{Chain: ffi.ChainConfig{Name: "c0"}, Weight: 1 << 63},
			},
			message: "failed to create ffi function config",
		},
		{
			name:     "weight sum above the bound",
			function: "f",
			chains: []ffi.FunctionChainConfig{
				{Chain: ffi.ChainConfig{Name: "c0"}, Weight: 40000},
				{Chain: ffi.ChainConfig{Name: "c1"}, Weight: 40000},
			},
			message: "exceeds the maximum",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(
				t,
				agent.UpdateFunction(ffi.FunctionConfig{
					Name:   tc.function,
					Chains: tc.chains,
				}),
				tc.message,
			)
		})
	}

	// A rejected update must leave the published config untouched.
	functions := shm.DPConfig(0).Functions()
	require.Len(t, functions, 1)
	require.Equal(t, "f", functions[0].Name)
	require.Len(t, functions[0].Chains, 1)
	require.Equal(t, "c0", functions[0].Chains[0].Name)
	require.Equal(t, uint64(1), functions[0].Chains[0].Weight)
}
