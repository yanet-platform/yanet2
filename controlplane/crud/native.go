package crud

import (
	"errors"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// nativeKinds pairs the refusals of the native control plane with the
// kinds the cgo one tags the same refusals with, which the statuses of a
// service map.
var nativeKinds = []struct {
	Refusal error
	Kind    error
}{
	{cpconfig.ErrNotFound, cerrors.NotFound},
	{cpconfig.ErrSupersededOwner, cerrors.FailedPrecondition},
	{cpconfig.ErrRepeated, cerrors.FailedPrecondition},
	{cpconfig.ErrMissingObject, cerrors.FailedPrecondition},
	{cpconfig.ErrMissingModule, cerrors.FailedPrecondition},
	{cpconfig.ErrInvalidName, cerrors.InvalidArgument},
	{cpconfig.ErrStillReferenced, cerrors.ErrStillReferenced},
	{shmalloc.ErrNoMemory, cerrors.ResourceExhausted},
}

// NativeError tags a refusal of the native control plane with the kind
// the cgo control plane reports for it.
//
// The refusals of a module's own builder that mean a wrong request are
// given as invalid and report an invalid argument. Any other error passes
// unchanged.
func NativeError(err error, invalid ...error) error {
	for _, refusal := range nativeKinds {
		if errors.Is(err, refusal.Refusal) {
			return &kindError{kind: refusal.Kind, err: err}
		}
	}
	for _, refusal := range invalid {
		if errors.Is(err, refusal) {
			return &kindError{kind: cerrors.InvalidArgument, err: err}
		}
	}
	return err
}

// kindError is an error tagged with a kind, both matching through the
// errors package, and reading as the error alone.
type kindError struct {
	kind error
	err  error
}

func (m *kindError) Error() string {
	return m.err.Error()
}

func (m *kindError) Unwrap() []error {
	return []error{m.kind, m.err}
}
