package bird_adapter_test

import (
	"net/netip"
	"testing"

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
		SourceV4: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1")),
		SourceV6: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::1")),
	}

	_, err := svc.SetupConfig(t.Context(), req)
	if err == nil {
		t.Fatal("expected an error for a request with no import config, got nil")
	}
}
