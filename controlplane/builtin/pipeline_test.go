package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	}

	svc := builtin.NewPipeline(0, nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.Update(t.Context(), test.request)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestPipelineDeleteRejectsMissingID verifies that Delete rejects a request
// with no id instead of deleting a pipeline named "".
func TestPipelineDeleteRejectsMissingID(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Delete(t.Context(), &ynpb.DeletePipelineRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestPipelineGetRejectsMissingID verifies that Get rejects a request with
// no id instead of dereferencing it.
func TestPipelineGetRejectsMissingID(t *testing.T) {
	svc := builtin.NewPipeline(0, nil)

	_, err := svc.Get(t.Context(), &ynpb.GetPipelineRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
