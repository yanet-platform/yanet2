package builtin_test

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// TestPipelineUpdateRejectsMissingMessages verifies that Update rejects a
// request whose optional message fields are absent instead of dereferencing
// them, and that the rejection happens before any shared memory is touched.
func TestPipelineUpdateRejectsMissingMessages(t *testing.T) {
	tests := []struct {
		name    string
		request *ynpb.UpdatePipelineRequest
	}{
		{
			name:    "missing pipeline",
			request: &ynpb.UpdatePipelineRequest{},
		},
		{
			name: "missing pipeline id",
			request: &ynpb.UpdatePipelineRequest{
				Pipeline: &ynpb.Pipeline{},
			},
		},
		{
			name: "nil function id in functions",
			request: &ynpb.UpdatePipelineRequest{
				Pipeline: &ynpb.Pipeline{
					Id:        &commonpb.PipelineId{Name: "p"},
					Functions: []*commonpb.FunctionId{nil},
				},
			},
		},
	}

	svc := builtin.NewPipeline(0, nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.Update(t.Context(), test.request)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// Test_Pipeline_Update_EmptyName verifies that Update rejects an id with
// an empty name instead of creating a pipeline named "".
func Test_Pipeline_Update_EmptyName(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Update(t.Context(), &ynpb.UpdatePipelineRequest{
		Pipeline: &ynpb.Pipeline{
			Id: &commonpb.PipelineId{},
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestPipelineDeleteRejectsMissingID verifies that Delete rejects a request
// with no id instead of deleting a pipeline named "".
func TestPipelineDeleteRejectsMissingID(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Delete(t.Context(), &ynpb.DeletePipelineRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_Pipeline_Delete_EmptyName verifies that Delete rejects an id with
// an empty name instead of deleting a pipeline named "".
func Test_Pipeline_Delete_EmptyName(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Delete(t.Context(), &ynpb.DeletePipelineRequest{
		Id: &commonpb.PipelineId{},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestPipelineGetRejectsMissingID verifies that Get rejects a request with
// no id instead of dereferencing it.
func TestPipelineGetRejectsMissingID(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Get(t.Context(), &ynpb.GetPipelineRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_Pipeline_UpdateAndDelete_Concurrent verifies that overlapping update
// and delete calls all succeed and leave the registry empty.
//
// Every call attaches an agent under one fixed name, and an attach reclaims
// the shared memory of the agent it supersedes, so calls that are not
// serialized would release an arena another call is still using.
func Test_Pipeline_UpdateAndDelete_Concurrent(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	svc := builtin.NewPipeline(0, harness.SharedMemory())

	const callers = 32
	const rounds = 64

	// Callers open their windows together and then repeat the round, so an
	// agent lifetime that is not serialized overlaps another one within a
	// single run.
	gate := make(chan struct{})

	group, ctx := errgroup.WithContext(t.Context())
	for caller := range callers {
		group.Go(func() error {
			id := &commonpb.PipelineId{Name: fmt.Sprintf("p%d", caller)}

			<-gate

			for range rounds {
				_, err := svc.Update(ctx, &ynpb.UpdatePipelineRequest{
					Pipeline: &ynpb.Pipeline{Id: id},
				})
				if err != nil {
					return err
				}
				if _, err := svc.Delete(ctx, &ynpb.DeletePipelineRequest{Id: id}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	close(gate)
	require.NoError(t, group.Wait())

	response, err := svc.List(t.Context(), &ynpb.ListPipelinesRequest{})
	require.NoError(t, err)
	require.Empty(t, response.GetIds())
}

// Test_Pipeline_Update_LeavesArenaSpare verifies that the agent an update
// leaves behind still reports spare room in its arena.
//
// Occupancy is reported as the reserved size minus what the allocator can
// still hand back, so an arena that yields no block at all reads as
// permanently full and puts this service at the top of the arena pressure
// readout while no call allocates from it at all.
func Test_Pipeline_Update_LeavesArenaSpare(t *testing.T) {
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
	svc := builtin.NewPipeline(0, shm)

	_, err = svc.Update(t.Context(), &ynpb.UpdatePipelineRequest{
		Pipeline: &ynpb.Pipeline{Id: &commonpb.PipelineId{Name: "spare"}},
	})
	require.NoError(t, err)

	var attached bool
	for _, agent := range shm.DPConfig(0).Agents() {
		if agent.Name != "pipeline" {
			continue
		}
		attached = true
		for _, instance := range agent.Instances {
			require.NotZero(t, instance.FreeBytes, "arena reports no spare room")
		}
	}
	require.True(t, attached, "update left no attached agent behind")
}
