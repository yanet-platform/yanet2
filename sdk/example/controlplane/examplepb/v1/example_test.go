package examplepb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	examplepb "github.com/yanet-platform/yanet2/sdk/example/controlplane/examplepb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty name is rejected
// and that a nil receiver reports the same error without panicking.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *examplepb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &examplepb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &examplepb.ShowConfigRequest{Name: "example0"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
