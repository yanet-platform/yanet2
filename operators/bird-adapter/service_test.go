package bird_adapter_test

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	birdadapter "github.com/yanet-platform/yanet2/operators/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
)

// Test_AdapterService_SetupConfig_Codes verifies that SetupConfig reports
// unusable import parameters as InvalidArgument and an unreachable route
// operator as Unavailable.
func Test_AdapterService_SetupConfig_Codes(t *testing.T) {
	sourceV4, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	sourceV6, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2001:db8::1"))
	require.NoError(t, err)
	mappedV6 := commonpb.NewIPv6Address(netip.MustParseAddr("::ffff:192.0.2.1").As16())

	cases := []struct {
		name     string
		sockets  []string
		sourceV6 *commonpb.IPv6Address
		code     codes.Code
	}{
		{
			name:     "no export sockets",
			sourceV6: sourceV6,
			code:     codes.InvalidArgument,
		},
		{
			name:     "IPv4-mapped v6 source",
			sockets:  []string{filepath.Join(t.TempDir(), "bird.sock")},
			sourceV6: mappedV6,
			code:     codes.InvalidArgument,
		},
		{
			name:     "unreachable route operator",
			sockets:  []string{filepath.Join(t.TempDir(), "bird.sock")},
			sourceV6: sourceV6,
			code:     codes.Unavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := "unix://" + filepath.Join(t.TempDir(), "route-operator.sock")
			svc := birdadapter.NewAdapterService(endpoint)

			_, err := svc.SetupConfig(t.Context(), &adapterpb.SetupConfigRequest{
				Name:     "bird0",
				Config:   &adapterpb.ImportConfig{Sockets: tc.sockets},
				SourceV4: sourceV4,
				SourceV6: tc.sourceV6,
			})
			require.Equal(t, tc.code, status.Code(err))
		})
	}
}
