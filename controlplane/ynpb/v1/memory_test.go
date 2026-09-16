package ynpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_ExtendAgentRequest_Validate verifies that an agent name and a positive
// extension size are required before shared-memory state is consulted.
func Test_ExtendAgentRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.ExtendAgentRequest
		message string
	}{
		{name: "nil request", request: nil, message: "agent is required"},
		{name: "missing agent", request: &ynpb.ExtendAgentRequest{Size: 1}, message: "agent is required"},
		{name: "zero size", request: &ynpb.ExtendAgentRequest{Agent: "agent0"}, message: "size must be positive"},
		{name: "positive size", request: &ynpb.ExtendAgentRequest{Agent: "agent0", Size: 1}},
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
