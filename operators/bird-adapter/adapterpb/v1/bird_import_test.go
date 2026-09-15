package adapterpb_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
)

// Test_SetupConfigRequest_Validate verifies that the import configuration and
// both source addresses are required before setup can proceed.
func Test_SetupConfigRequest_Validate(t *testing.T) {
	validConfig := &adapterpb.ImportConfig{}
	validV4 := commonpb.NewIPv4Address(netip.MustParseAddr("10.0.0.1").As4())
	validV6 := commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::1").As16())

	cases := []struct {
		name    string
		request *adapterpb.SetupConfigRequest
		message string
	}{
		{
			name: "missing config",
			request: &adapterpb.SetupConfigRequest{
				SourceV4: validV4,
				SourceV6: validV6,
			},
			message: "config is required",
		},
		{
			name: "missing source_v4",
			request: &adapterpb.SetupConfigRequest{
				Config:   validConfig,
				SourceV6: validV6,
			},
			message: "source_v4 is required",
		},
		{
			name: "missing source_v6",
			request: &adapterpb.SetupConfigRequest{
				Config:   validConfig,
				SourceV4: validV4,
			},
			message: "source_v6 is required",
		},
		{
			name:    "nil request",
			message: "config is required",
		},
		{
			name: "all required fields present",
			request: &adapterpb.SetupConfigRequest{
				Config:   validConfig,
				SourceV4: validV4,
				SourceV6: validV6,
			},
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
