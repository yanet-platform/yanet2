package agenterr_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/agenterr"
)

// Test_ClassifyDelete_Cases verifies that a missing-entity leaf maps to
// NotFound and every other backend failure maps to Internal.
//
// An in-use refusal shares the same "not found" wording as a
// missing-entity leaf but continues past it, so it must stay Internal;
// both outcomes keep the message unchanged.
func Test_ClassifyDelete_Cases(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		code codes.Code
	}{
		{
			name: "missing pipeline",
			msg:  `failed to delete pipeline "p": pipeline 'p' not found`,
			code: codes.NotFound,
		},
		{
			name: "missing function",
			msg:  `failed to delete function "f": function 'f' not found`,
			code: codes.NotFound,
		},
		{
			name: "pipeline attached to a device",
			msg:  `failed to delete pipeline "p": pipeline 'p' not found in device entry`,
			code: codes.Internal,
		},
		{
			name: "function still used by a pipeline",
			msg:  `failed to delete function "f": function 'f' not found for pipeline 'p'`,
			code: codes.Internal,
		},
		{
			name: "registry delete failure",
			msg:  `failed to delete function "f": failed to delete function from registry`,
			code: codes.Internal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := agenterr.ClassifyDelete(errors.New(test.msg))

			require.Equal(t, test.code, status.Code(err))
			require.Equal(t, test.msg, status.Convert(err).Message())
		})
	}
}

// Test_ClassifyUpdate_Cases verifies that a missing referenced entity
// leaf maps to NotFound and every other backend failure maps to Internal.
//
// Both the missing-function and missing-chain-module leaves map to
// NotFound regardless of which RPC's message prefix carries them; every
// outcome keeps the message unchanged.
func Test_ClassifyUpdate_Cases(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		code codes.Code
	}{
		{
			name: "pipeline references missing function",
			msg:  `failed to update pipelines: function 'f' not found for pipeline 'p'`,
			code: codes.NotFound,
		},
		{
			name: "function chain names missing module",
			msg:  `failed to update functions: module 't:n' not found in chain 'c' of function 'f' in pipeline 'p'`,
			code: codes.NotFound,
		},
		{
			name: "pipeline update surfaces a chain-module leaf",
			msg:  `failed to update pipelines: module 't:n' not found in chain 'c' of function 'f' in pipeline 'p'`,
			code: codes.NotFound,
		},
		{
			name: "allocation failure",
			msg:  `failed to create pipeline config: failed to create ffi pipeline config`,
			code: codes.Internal,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := agenterr.ClassifyUpdate(errors.New(test.msg))

			require.Equal(t, test.code, status.Code(err))
			require.Equal(t, test.msg, status.Convert(err).Message())
		})
	}
}
