package crud_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_NativeError_TagsKind verifies that a native refusal, wrapped or not,
// matches the kind the cgo control plane reports for it and still matches
// itself.
func Test_NativeError_TagsKind(t *testing.T) {
	errInvalidRule := errors.New("invalid rule")
	cases := []struct {
		name    string
		refusal error
		kind    error
	}{
		{name: "nothing under the name", refusal: cpconfig.ErrNotFound, kind: cerrors.NotFound},
		{name: "superseded owner", refusal: cpconfig.ErrSupersededOwner, kind: cerrors.FailedPrecondition},
		{name: "repeated config", refusal: cpconfig.ErrRepeated, kind: cerrors.FailedPrecondition},
		{name: "missing linked object", refusal: cpconfig.ErrMissingObject, kind: cerrors.FailedPrecondition},
		{name: "missing chained module", refusal: cpconfig.ErrMissingModule, kind: cerrors.FailedPrecondition},
		{name: "invalid name", refusal: cpconfig.ErrInvalidName, kind: cerrors.InvalidArgument},
		{name: "still referenced", refusal: cpconfig.ErrStillReferenced, kind: cerrors.ErrStillReferenced},
		{name: "memory exhausted", refusal: shmalloc.ErrNoMemory, kind: cerrors.ResourceExhausted},
		{name: "module's own invalid argument", refusal: errInvalidRule, kind: cerrors.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := crud.NativeError(fmt.Errorf("build: %w", tc.refusal), errInvalidRule)

			require.ErrorIs(t, err, tc.kind)
			require.ErrorIs(t, err, tc.refusal)
		})
	}
}

// Test_NativeError_PassesOtherErrors verifies that an error no table names,
// nor the module's own refusals, comes back unchanged.
func Test_NativeError_PassesOtherErrors(t *testing.T) {
	err := errors.New("unrelated")

	require.Same(t, err, crud.NativeError(err, errors.New("invalid rule")))
}
