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

// TestFunctionUpdateRejectsMissingMessages verifies that Update rejects a
// request whose optional message fields are absent instead of dereferencing
// them, and that the rejection happens before any shared memory is touched.
func TestFunctionUpdateRejectsMissingMessages(t *testing.T) {
	tests := []struct {
		name    string
		request *ynpb.UpdateFunctionRequest
	}{
		{
			name:    "missing function",
			request: &ynpb.UpdateFunctionRequest{},
		},
		{
			name: "missing function id",
			request: &ynpb.UpdateFunctionRequest{
				Function: &ynpb.Function{},
			},
		},
		{
			name: "missing chain",
			request: &ynpb.UpdateFunctionRequest{
				Function: &ynpb.Function{
					Id: &commonpb.FunctionId{Name: "f"},
					Chains: []*ynpb.FunctionChain{
						{Weight: 1},
					},
				},
			},
		},
	}

	svc := builtin.NewFunction(0, nil)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.Update(t.Context(), test.request)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestFunctionDeleteRejectsMissingID verifies that Delete rejects a request
// with no id instead of dereferencing it to build the function name.
func TestFunctionDeleteRejectsMissingID(t *testing.T) {
	svc := builtin.NewFunction(0, nil)

	_, err := svc.Delete(t.Context(), &ynpb.DeleteFunctionRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestFunctionGetRejectsMissingID verifies that Get rejects a request with
// no id instead of dereferencing it.
func TestFunctionGetRejectsMissingID(t *testing.T) {
	svc := builtin.NewFunction(0, nil)

	_, err := svc.Get(t.Context(), &ynpb.GetFunctionRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
