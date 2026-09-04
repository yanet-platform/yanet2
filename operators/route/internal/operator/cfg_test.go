package operator_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in operator.Config.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-route-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies
// that the shipped file inherits the loopback port-zero listener endpoint.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[operator.Config](
		"../../etc/yanet/yanet-route-operator-default.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
	require.Equal(t, operator.DefaultNetlinkSidecarUpdateTimeout, config.NetlinkSidecar.UpdateTimeout)
}

// Test_Config_Validate_StaticInterfaceRequiredOnlyForEnabledSidecar verifies
// that legacy static routes remain valid until sidecar publication is enabled.
func Test_Config_Validate_StaticInterfaceRequiredOnlyForEnabledSidecar(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		interfaces []string
		wantErr    string
	}{
		{name: "disabled sidecar without interface", interfaces: []string{""}},
		{
			name:       "enabled sidecar with later route missing interface",
			enabled:    true,
			interfaces: []string{"eth0", ""},
			wantErr:    "static route 1: interface is required",
		},
		{
			name:       "enabled sidecar with every interface",
			enabled:    true,
			interfaces: []string{"eth0", "eth1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := operator.DefaultConfig()
			config.Gateways = []commonoperator.GatewayConfig{{
				Name:     "numa0",
				Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
			}}
			config.NetlinkSidecar.Enabled = test.enabled
			for _, interfaceName := range test.interfaces {
				config.Static.Routes = append(config.Static.Routes, operator.StaticRouteConfig{
					Prefix:      "192.0.2.0/24",
					NexthopAddr: "192.0.2.1",
					Interface:   interfaceName,
				})
			}

			err := config.Validate()
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func Test_Config_Validate_EnabledSidecarRequiresPositiveUpdateTimeout(t *testing.T) {
	config := operator.DefaultConfig()
	config.Gateways = []commonoperator.GatewayConfig{{
		Name:     "numa0",
		Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
	}}
	config.NetlinkSidecar.Enabled = true
	config.NetlinkSidecar.UpdateTimeout = 0

	require.ErrorContains(t, config.Validate(), "update_timeout must be positive")
}

func Test_Config_Validate_EnabledSidecarRejectsCanonicalDuplicateRoutes(t *testing.T) {
	config := operator.DefaultConfig()
	config.Gateways = []commonoperator.GatewayConfig{{
		Name:     "numa0",
		Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
	}}
	config.NetlinkSidecar.Enabled = true
	config.Static.Routes = []operator.StaticRouteConfig{
		{Prefix: "192.0.2.0/24", NexthopAddr: "192.0.2.1", Interface: "eth0"},
		{Prefix: "192.0.2.1/24", NexthopAddr: "192.0.2.1", Interface: "eth0"},
	}

	require.ErrorContains(t, config.Validate(), "duplicates static route 0")
}

func Test_Config_Validate_EnabledSidecarRejectsIPv4MappedRoutes(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		nexthop       string
		errorContains string
	}{
		{
			name:          "prefix",
			prefix:        "::ffff:192.0.2.0/120",
			nexthop:       "::ffff:192.0.2.1",
			errorContains: "IPv4-mapped IPv6 prefix",
		},
		{
			name:          "nexthop",
			prefix:        "2001:db8::/64",
			nexthop:       "::ffff:192.0.2.1",
			errorContains: "IPv4-mapped IPv6 address",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := operator.DefaultConfig()
			config.Gateways = []commonoperator.GatewayConfig{{
				Name:     "numa0",
				Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
			}}
			config.NetlinkSidecar.Enabled = true
			config.Static.Routes = []operator.StaticRouteConfig{{
				Prefix:      test.prefix,
				NexthopAddr: test.nexthop,
				Interface:   "kni0",
			}}

			require.ErrorContains(t, config.Validate(), test.errorContains)
		})
	}
}
