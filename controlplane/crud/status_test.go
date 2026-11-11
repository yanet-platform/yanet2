package crud_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_PublishError_MapsFailures verifies that each failed update kind
// reports its own gRPC status and that a status the mutation produced
// passes through.
func Test_PublishError_MapsFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "status from the mutation", err: status.Error(codes.InvalidArgument, "bad prefix"), code: codes.InvalidArgument},
		{name: "dataplane refusal", err: ffi.ErrFailedPrecondition, code: codes.FailedPrecondition},
		{name: "no room left", err: ffi.ErrResourceExhausted, code: codes.ResourceExhausted},
		{name: "refused input", err: ffi.ErrInvalidArgument, code: codes.InvalidArgument},
		{name: "anything else", err: errors.New("boom"), code: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.code, status.Code(crud.PublishError("wan", tc.err)))
		})
	}
}
