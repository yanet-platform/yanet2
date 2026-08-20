package yncp_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in yncp.Config, including one nested
// inside a module or device block whose own UnmarshalYAML re-decodes
// through a fresh yaml.v3 decoder.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../etc/yanet/controlplane.d/default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[yncp.Config](data))
}

// Test_ShippedDefaultConfig_LoadsIntendedEnabledSet asserts that the shipped
// default config starts all ten bundled modules plus the plain and vlan
// devices, leaves trafgen disabled, and sets an explicit gateway instance.
func Test_ShippedDefaultConfig_LoadsIntendedEnabledSet(t *testing.T) {
	cfg, err := xcfg.LoadConfig[yncp.Config]("../etc/yanet/controlplane.d/default.yaml")
	require.NoError(t, err)

	require.NotNil(t, cfg.Modules.Route.Unwrap())
	require.NotNil(t, cfg.Modules.RouteMPLS.Unwrap())
	require.NotNil(t, cfg.Modules.Decap.Unwrap())
	require.NotNil(t, cfg.Modules.DSCP.Unwrap())
	require.NotNil(t, cfg.Modules.Forward.Unwrap())
	require.NotNil(t, cfg.Modules.Mirror.Unwrap())
	require.NotNil(t, cfg.Modules.NAT64.Unwrap())
	require.NotNil(t, cfg.Modules.Pdump.Unwrap())
	require.NotNil(t, cfg.Modules.ACL.Unwrap())
	require.NotNil(t, cfg.Modules.Blackhole.Unwrap())

	require.NotNil(t, cfg.Devices.Plain.Unwrap())
	require.NotNil(t, cfg.Devices.Vlan.Unwrap())
	require.Nil(t, cfg.Devices.Trafgen.Unwrap())

	require.NoError(t, cfg.Gateway.InstanceID.Validate())
	require.Equal(t, uint32(0), cfg.Gateway.InstanceID.Unwrap())
}

// Test_ShippedDefaultConfig_OmittedBackendEndpointsUseEphemeralPorts verifies
// that omitted backend endpoints inherit loopback port-zero defaults.
func Test_ShippedDefaultConfig_OmittedBackendEndpointsUseEphemeralPorts(t *testing.T) {
	config, err := xcfg.LoadConfig[yncp.Config](
		"../etc/yanet/controlplane.d/default.yaml",
	)
	require.NoError(t, err)

	endpoints := map[string]string{
		"route module":      config.Modules.Route.Unwrap().Endpoint.Unwrap(),
		"route MPLS module": config.Modules.RouteMPLS.Unwrap().Endpoint.Unwrap(),
		"decap module":      config.Modules.Decap.Unwrap().Endpoint.Unwrap(),
		"DSCP module":       config.Modules.DSCP.Unwrap().Endpoint.Unwrap(),
		"forward module":    config.Modules.Forward.Unwrap().Endpoint.Unwrap(),
		"mirror module":     config.Modules.Mirror.Unwrap().Endpoint.Unwrap(),
		"NAT64 module":      config.Modules.NAT64.Unwrap().Endpoint.Unwrap(),
		"pdump module":      config.Modules.Pdump.Unwrap().Endpoint.Unwrap(),
		"ACL module":        config.Modules.ACL.Unwrap().Endpoint.Unwrap(),
		"blackhole module":  config.Modules.Blackhole.Unwrap().Endpoint.Unwrap(),
		"plain device":      config.Devices.Plain.Unwrap().Endpoint.Unwrap(),
		"VLAN device":       config.Devices.Vlan.Unwrap().Endpoint.Unwrap(),
	}
	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, "[::1]:0", endpoint)
		})
	}
}

// Test_ModuleWithoutInstanceID_FailsValidation asserts that a listed module
// omitting instance_id fails with a dotted path naming that module.
func Test_ModuleWithoutInstanceID_FailsValidation(t *testing.T) {
	input := "gateway:\n  instance_id: 0\n" +
		"modules:\n  route: {}\n"
	cfg := yncp.DefaultConfig()
	err := xcfg.Decode([]byte(input), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.route.instance_id")
}

// Test_GatewayWithoutInstanceID_FailsValidation asserts that a gateway block
// omitting instance_id fails with a dotted path naming the gateway.
func Test_GatewayWithoutInstanceID_FailsValidation(t *testing.T) {
	var cfg yncp.Config
	err := xcfg.Decode([]byte("gateway: {}\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gateway.instance_id")
}

// Test_ExplicitNullGateway_FailsValidation asserts that a bare "gateway:" key
// or an explicit "gateway: null" is rejected by the null-block check.
//
// The field is a plain value, so a cleared block is no longer representable
// and the rejection needs no hand-written validation. A null block used to
// decode into a nil pointer that panicked on the first dereference; the
// loader's null-block check now fails the document with the key and line.
func Test_ExplicitNullGateway_FailsValidation(t *testing.T) {
	for name, input := range map[string]string{
		"bare key":   "gateway:\n",
		"null value": "gateway: null\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := yncp.DefaultConfig()
			err := xcfg.Decode([]byte(input), cfg)
			require.Error(t, err)
			require.Equal(t, `key "gateway" at line 1 has no value; give it a body or remove the key`, err.Error())
		})
	}
}

// Test_NullTopLevelModules_FailsValidation asserts that a null top-level "modules:" block is rejected naming modules and its line.
func Test_NullTopLevelModules_FailsValidation(t *testing.T) {
	cfg := yncp.DefaultConfig()
	err := xcfg.Decode([]byte("gateway:\n  instance_id: 0\nmodules:\n"), cfg)
	require.Error(t, err)
	require.Equal(t, `key "modules" at line 3 has no value; give it a body or remove the key`, err.Error())
}

// Test_NullTopLevelDevices_FailsValidation asserts that a null top-level "devices:" block is rejected naming devices and its line.
func Test_NullTopLevelDevices_FailsValidation(t *testing.T) {
	cfg := yncp.DefaultConfig()
	err := xcfg.Decode([]byte("gateway:\n  instance_id: 0\ndevices:\n"), cfg)
	require.Error(t, err)
	require.Equal(t, `key "devices" at line 3 has no value; give it a body or remove the key`, err.Error())
}

// Test_ModuleBlock_StrayNestedKey_RejectedByKnownFields asserts that a key
// nested inside a module's own block, not just at the document's top level,
// is rejected when loading through the director's WithKnownFields path.
func Test_ModuleBlock_StrayNestedKey_RejectedByKnownFields(t *testing.T) {
	input := "modules:\n  decap:\n" +
		"    instance_id: 0\n" +
		"    memory_path: /dev/hugepages/yanet\n" +
		"    memory_requirements: 16MB\n" +
		"    endpoint: \"[::1]:0\"\n" +
		"    bogus: z\n"
	var cfg yncp.Config
	err := xcfg.Decode([]byte(input), &cfg, xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.decap.bogus")
}
