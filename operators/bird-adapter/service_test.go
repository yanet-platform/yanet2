package bird_adapter_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	bird_adapter "github.com/yanet-platform/yanet2/operators/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
)

// TestAdapterService_SetupConfig_RejectsMissingConfig asserts that a request
// with no import config returns an error instead of panicking.
func TestAdapterService_SetupConfig_RejectsMissingConfig(t *testing.T) {
	svc := bird_adapter.NewAdapterService("127.0.0.1:0")

	req := &adapterpb.SetupConfigRequest{
		Name:     "test",
		SourceV4: commonpb.NewIPv4Address(netip.MustParseAddr("10.0.0.1").As4()),
		SourceV6: commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::1").As16()),
	}

	_, err := svc.SetupConfig(t.Context(), req)
	if err == nil {
		t.Fatal("expected an error for a request with no import config, got nil")
	}
}

// TestAdapterService_SetupConfig_RejectsMissingSource asserts that omitting
// either source address fails on the presence check specifically.
//
// Every case errors eventually — the route operator endpoint is unreachable —
// so the assertions pin down whether the presence check itself fired.
func TestAdapterService_SetupConfig_RejectsMissingSource(t *testing.T) {
	validV4 := commonpb.NewIPv4Address(netip.MustParseAddr("10.0.0.1").As4())
	validV6 := commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::1").As16())

	tests := []struct {
		name      string
		sourceV4  *commonpb.IPv4Address
		sourceV6  *commonpb.IPv6Address
		wantError string
	}{
		{
			name:      "missing v4 source",
			sourceV6:  validV6,
			wantError: "no v4 source address provided",
		},
		{
			name:      "missing v6 source",
			sourceV4:  validV4,
			wantError: "no v6 source address provided",
		},
		{
			name:     "both sources present pass the presence checks",
			sourceV4: validV4,
			sourceV6: validV6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := bird_adapter.NewAdapterService("127.0.0.1:0")

			req := &adapterpb.SetupConfigRequest{
				Name:     "test",
				SourceV4: tc.sourceV4,
				SourceV6: tc.sourceV6,
				Config: &adapterpb.ImportConfig{
					Sockets: []string{"/nonexistent.sock"},
				},
			}

			_, err := svc.SetupConfig(t.Context(), req)
			require.Error(t, err)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NotContains(t, err.Error(), "source address provided")
		})
	}
}
