package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
