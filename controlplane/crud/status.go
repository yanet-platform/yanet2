package crud

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// PublishError maps a failed update of the named config to the gRPC
// status it reports.
//
// A status the mutation itself produced passes through. A config the
// dataplane refuses in its current state reports FailedPrecondition, one
// it has no room for reports ResourceExhausted, one refused as wrong in
// any state reports InvalidArgument, and anything else reports Internal.
func PublishError(name string, err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}

	code := codes.Internal
	switch {
	case errors.Is(err, ffi.ErrFailedPrecondition):
		code = codes.FailedPrecondition
	case errors.Is(err, ffi.ErrResourceExhausted):
		code = codes.ResourceExhausted
	case errors.Is(err, ffi.ErrInvalidArgument):
		code = codes.InvalidArgument
	}
	return status.Errorf(code, "failed to update config %q: %v", name, err)
}
