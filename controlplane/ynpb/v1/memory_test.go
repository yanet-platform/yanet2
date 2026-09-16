package ynpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_ExtendAgentRequest_Validate verifies that the agent name obeys the
// fixed-size buffer and NUL rules and that a positive extension size is required.
func Test_ExtendAgentRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.ExtendAgentRequest
		message string
	}{
		{name: "nil request", request: nil, message: "agent is required"},
		{name: "missing agent", request: &ynpb.ExtendAgentRequest{Size: 1}, message: "agent is required"},
		{
			name:    "agent with NUL",
			request: &ynpb.ExtendAgentRequest{Agent: "agent0\x00other", Size: 1},
			message: "agent must not contain NUL",
		},
		{
			name:    "agent of the buffer size",
			request: &ynpb.ExtendAgentRequest{Agent: strings.Repeat("a", ynpb.MaxAgentNameLen), Size: 1},
			message: "agent must be shorter than 80 bytes",
		},
		{
			name:    "longest agent",
			request: &ynpb.ExtendAgentRequest{Agent: strings.Repeat("a", ynpb.MaxAgentNameLen-1), Size: 1},
		},
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
