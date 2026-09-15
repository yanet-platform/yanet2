package ynpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_RegisterRequest_Validate verifies that registration reports a missing
// backend or field with the nested field path, including for nil messages.
func Test_RegisterRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.RegisterRequest
		message string
	}{
		{
			name:    "missing backend",
			request: &ynpb.RegisterRequest{},
			message: "backend is required",
		},
		{
			name:    "nil request",
			request: nil,
			message: "backend is required",
		},
		{
			name: "empty backend name",
			request: &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
				Endpoint: "passthrough:test-endpoint",
			}},
			message: "backend: name is required",
		},
		{
			name: "empty backend endpoint",
			request: &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
				Name: "svc.Test",
			}},
			message: "backend: endpoint is required",
		},
		{
			name: "valid backend",
			request: &ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
				Name: "svc.Test", Endpoint: "passthrough:test-endpoint",
			}},
		},
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

// Test_BackendDesc_Validate verifies that a backend descriptor requires both
// the service name and the endpoint, including for a nil descriptor.
func Test_BackendDesc_Validate(t *testing.T) {
	cases := []struct {
		name    string
		backend *ynpb.BackendDesc
		message string
	}{
		{
			name:    "empty name",
			backend: &ynpb.BackendDesc{Endpoint: "passthrough:test-endpoint"},
			message: "name is required",
		},
		{
			name:    "nil backend",
			backend: nil,
			message: "name is required",
		},
		{
			name:    "empty endpoint",
			backend: &ynpb.BackendDesc{Name: "svc.Test"},
			message: "endpoint is required",
		},
		{
			name:    "valid backend",
			backend: &ynpb.BackendDesc{Name: "svc.Test", Endpoint: "passthrough:test-endpoint"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.backend.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
