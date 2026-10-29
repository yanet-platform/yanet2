package crud

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// UpdateError maps a failed update to the gRPC status it reports.
//
// A config the dataplane refuses to publish — the graph it runs names
// an entity it cannot resolve — reports FailedPrecondition; anything
// else reports Internal.
func UpdateError(err error) error {
	if errors.Is(err, ffi.ErrFailedPrecondition) {
		return status.Errorf(codes.FailedPrecondition, "failed to update device: %v", err)
	}
	return status.Errorf(codes.Internal, "failed to update device: %v", err)
}

// MutationError maps a failed mutation to the gRPC status it reports: a
// status the mutation itself produced passes through, a backend failure
// reports Internal.
func MutationError(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Internal, "failed to update module config: %v", err)
}
