package operator_test

import (
	"fmt"
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
			for idx, interfaceName := range test.interfaces {
				config.Static.Routes = append(config.Static.Routes, operator.StaticRouteConfig{
					Prefix:      fmt.Sprintf("192.0.%d.0/24", idx+2),
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

// Test_Config_Validate_EnabledSidecarRequiresPositiveUpdateTimeout verifies that
// an enabled static-route publisher always has a finite positive RPC budget.
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

// Test_Config_Validate_EnabledSidecarRejectsCanonicalDuplicateRoutes verifies that
// host bits cannot disguise duplicate static routes in one complete snapshot.
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

// Test_Config_Validate_EnabledSidecarRejectsIPv4MappedRoutes verifies that static
// addresses cannot lose their configured family during wire conversion.
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

// Test_Config_Validate_RejectsNULInterface verifies that a malformed Linux
// interface is rejected before any sidecar publication can start.
func Test_Config_Validate_RejectsNULInterface(t *testing.T) {
	config := operator.DefaultConfig()
	config.Gateways = []commonoperator.GatewayConfig{{
		Name:     "numa0",
		Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
	}}
	config.NetlinkSidecar.Enabled = true
	config.Static.Routes = []operator.StaticRouteConfig{{
		Prefix:      "192.0.2.0/24",
		NexthopAddr: "192.0.2.1",
		Interface:   "eth\x000",
	}}

	require.ErrorContains(t, config.Validate(), "interface contains a NUL byte")
}

// Test_Config_Validate_RejectsAmbiguousStaticIdentity verifies that one static
// RIB identity cannot silently replace a route bound to a different interface.
func Test_Config_Validate_RejectsAmbiguousStaticIdentity(t *testing.T) {
	config := operator.DefaultConfig()
	config.Gateways = []commonoperator.GatewayConfig{{
		Name:     "numa0",
		Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
	}}
	config.NetlinkSidecar.Enabled = true
	config.Static.Routes = []operator.StaticRouteConfig{
		{Prefix: "2001:db8::/64", NexthopAddr: "fe80::1", Interface: "kni0"},
		{Prefix: "2001:db8::1/64", NexthopAddr: "fe80::1", Interface: "kni1"},
	}

	require.ErrorContains(t, config.Validate(), "same prefix and nexthop")
	require.ErrorContains(t, config.Validate(), "different interfaces")
}

// Test_Config_Validate_RejectsEmptyStaticDevice verifies that link-name mapping
// cannot silently remove a configured route's egress constraint.
func Test_Config_Validate_RejectsEmptyStaticDevice(t *testing.T) {
	config := operator.DefaultConfig()
	config.Gateways = []commonoperator.GatewayConfig{{
		Name:     "numa0",
		Endpoint: xcfg.MustNonEmptyString("127.0.0.1:8080"),
	}}
	config.NetlinkSidecar.Enabled = true
	config.LinkMap = map[string]string{"kni0": ""}
	config.Static.Routes = []operator.StaticRouteConfig{{
		Prefix: "192.0.2.0/24", NexthopAddr: "192.0.2.1", Interface: "kni0",
	}}

	require.ErrorContains(t, config.Validate(), "maps to an empty device")
}
