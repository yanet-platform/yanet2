package operator_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/generic/operator"

	_ "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	_ "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// shippedInstanceConfigs lists the instance configs the package installs
// under /etc/yanet2/generic-operator.d, relative to this test.
var shippedInstanceConfigs = []string{
	"../etc/yanet/generic-operator.d/forward.yaml",
	"../etc/yanet/generic-operator.d/decap.yaml",
}

// Test_ShippedInstanceConfigs_NoUnknownKeys guards the shipped instance
// configs against a key that matches no field in Config.
//
// Keys inside a target's function are opaque to this walk and are
// guarded by the end-to-end load test instead.
func Test_ShippedInstanceConfigs_NoUnknownKeys(t *testing.T) {
	for _, path := range shippedInstanceConfigs {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
		})
	}
}

// Test_ShippedInstanceConfigs_LoadEndToEnd verifies that every shipped
// instance config decodes end to end.
//
// Every target's function must decode into a chained ynpb.Function, and
// every referenced module config file must decode against the target's
// spelled method.
func Test_ShippedInstanceConfigs_LoadEndToEnd(t *testing.T) {
	for _, path := range shippedInstanceConfigs {
		t.Run(path, func(t *testing.T) {
			cfg, err := xcfg.LoadConfig[operator.Config](path)
			require.NoError(t, err)
			require.NotEmpty(t, cfg.Targets)

			for _, target := range cfg.Targets {
				function := target.Function.Unwrap()
				require.NotNil(t, function)
				require.NotEmpty(t, function.GetId().GetName())
				require.NotEmpty(t, function.GetChains())

				// The shipped file references install paths, which the
				// repository keeps under etc/yanet.
				file := strings.Replace(
					target.File.Unwrap(), "/etc/yanet2/", "../etc/yanet/", 1,
				)
				request, err := operator.LoadRequest(target.Method.Unwrap(), file)
				require.NoError(t, err)
				require.NotNil(t, request)
			}
		})
	}
}

// Test_TargetConfig_IgnorePdump_DefaultsTrue verifies that an omitted
// ignore_pdump key defaults to true.
func Test_TargetConfig_IgnorePdump_DefaultsTrue(t *testing.T) {
	raw := `
name: decap0
method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
file: /etc/yanet2/decap.d/default.yaml
`
	var target operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &target))
	require.True(t, target.IgnorePdump)
}

// Test_TargetConfig_IgnorePdump_ExplicitFalse verifies that an explicit
// ignore_pdump false is preserved.
func Test_TargetConfig_IgnorePdump_ExplicitFalse(t *testing.T) {
	raw := `
name: decap0
method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
file: /etc/yanet2/decap.d/default.yaml
ignore_pdump: false
`
	var target operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &target))
	require.False(t, target.IgnorePdump)
}

// Test_FunctionConfig_DecodesWholeFunction verifies that a function node
// decodes through xproto into the whole ynpb.Function.
func Test_FunctionConfig_DecodesWholeFunction(t *testing.T) {
	raw := `
id:
  name: fn:decap
chains:
  - chain:
      name: default
      modules:
        - type: decap
          name: decap0
    weight: 2
`
	var function operator.FunctionConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &function))

	decoded := function.Unwrap()
	require.Equal(t, "fn:decap", decoded.GetId().GetName())
	require.Len(t, decoded.GetChains(), 1)
	require.Equal(t, uint64(2), decoded.GetChains()[0].GetWeight())
	chain := decoded.GetChains()[0].GetChain()
	require.Equal(t, "default", chain.GetName())
	require.Len(t, chain.GetModules(), 1)
	require.Equal(t, "decap", chain.GetModules()[0].GetType())
	require.Equal(t, "decap0", chain.GetModules()[0].GetName())
}

// Test_FunctionConfig_RejectsUnknownKey verifies that a key outside
// ynpb.Function is rejected, not ignored.
func Test_FunctionConfig_RejectsUnknownKey(t *testing.T) {
	raw := `
id:
  name: fn:decap
chainz: []
`
	var function operator.FunctionConfig
	err := yaml.Unmarshal([]byte(raw), &function)
	require.ErrorContains(t, err, "chainz")
}

// Test_Config_RejectsDuplicateGateways verifies that two gateways sharing
// a name are refused at decode time.
func Test_Config_RejectsDuplicateGateways(t *testing.T) {
	raw := `
name: forward
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
  - name: numa0
    endpoint: "[::1]:8082"
targets:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
`
	cfg := operator.DefaultConfig()
	err := xcfg.Decode([]byte(raw), cfg)
	require.ErrorContains(t, err, `duplicate gateway name "numa0"`)
}
