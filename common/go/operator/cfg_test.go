package operator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// verifies that valid names and addresses pass without resolving their hosts,
// while missing hosts and invalid TCP ports fail at startup.
func Test_GRPCServerConfig_Validate_AdvertiseEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "unset", endpoint: ""},
		{name: "DNS name", endpoint: "route-operator.yanet:8080"},
		{name: "IPv6 literal", endpoint: "[2001:db8::1]:8080"},
		{name: "missing port", endpoint: "route-operator.yanet", wantErr: true},
		{name: "empty host", endpoint: ":8080", wantErr: true},
		{name: "empty port", endpoint: "route-operator.yanet:", wantErr: true},
		{name: "zero port", endpoint: "route-operator.yanet:0", wantErr: true},
		{name: "out-of-range port", endpoint: "route-operator.yanet:65536", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := operator.GRPCServerConfig{AdvertiseEndpoint: test.endpoint}

			err := cfg.Validate()

			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
