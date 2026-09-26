package operator_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// Test_ApplyGatewayOverrides_SelectsNamedTransports verifies that explicit
// ordering and address changes preserve transport identity and security.
func Test_ApplyGatewayOverrides_SelectsNamedTransports(t *testing.T) {
	gateways := []operator.GatewayConfig{
		{Name: "numa0", Endpoint: xcfg.MustNonEmptyString("old-zero:9000")},
		{Name: "numa1", Endpoint: xcfg.MustNonEmptyString("old-one:9001"), TLS: &xgrpc.ClientTLSConfig{
			CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "gateway.example",
		}},
		{Name: "numa2", Endpoint: xcfg.MustNonEmptyString("old-two:9002")},
	}
	before := slices.Clone(gateways)
	selected, err := operator.ApplyGatewayOverrides(gateways, []operator.GatewayEndpointOverride{
		{Name: "numa1", Endpoint: "[2001:db8::1]:65535"},
		{Name: "numa0", Endpoint: "gateway-zero.test.svc:8080"},
	})
	require.NoError(t, err)
	require.Equal(t, []operator.GatewayConfig{
		{Name: "numa1", Endpoint: xcfg.MustNonEmptyString("[2001:db8::1]:65535"), TLS: &xgrpc.ClientTLSConfig{
			CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client.key", ServerName: "gateway.example",
		}},
		{Name: "numa0", Endpoint: xcfg.MustNonEmptyString("gateway-zero.test.svc:8080")},
	}, selected)
	require.Equal(t, before, gateways)
}

// Test_ApplyGatewayOverrides_RejectsAmbiguousInput verifies that invalid names
// or dial addresses fail atomically, without partially changing transports.
func Test_ApplyGatewayOverrides_RejectsAmbiguousInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		names     []string
		overrides []operator.GatewayEndpointOverride
	}{
		{name: "empty active set", names: []string{"numa0"}},
		{name: "missing configured name", names: []string{""}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:8080"}}},
		{name: "duplicate configured name", names: []string{"numa0", "numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:8080"}}},
		{name: "missing override name", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Endpoint: "gateway:8080"}}},
		{name: "unknown override after valid selection", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:8080"}, {Name: "numa1", Endpoint: "gateway:8080"}}},
		{name: "duplicate override", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:8080"}, {Name: "numa0", Endpoint: "gateway:8081"}}},
		{name: "empty endpoint", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0"}}},
		{name: "missing host", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: ":8080"}}},
		{name: "missing port", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway"}}},
		{name: "zero port", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:0"}}},
		{name: "port exceeds TCP range", names: []string{"numa0"}, overrides: []operator.GatewayEndpointOverride{{Name: "numa0", Endpoint: "gateway:65536"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gateways []operator.GatewayConfig
			for _, name := range tc.names {
				gateways = append(gateways, operator.GatewayConfig{Name: name, Endpoint: xcfg.MustNonEmptyString("original:9000")})
			}
			before := slices.Clone(gateways)
			selected, err := operator.ApplyGatewayOverrides(gateways, tc.overrides)
			require.Error(t, err)
			require.Nil(t, selected)
			require.Equal(t, before, gateways)
		})
	}
}
