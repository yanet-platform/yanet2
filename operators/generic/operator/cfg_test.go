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
// configs against unknown keys, including inside a target's function.
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
				function := target.Function.AsFunction()
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

// Test_FunctionConfig_DecodesWholeFunction verifies that a spelled
// function converts into the whole ynpb.Function.
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
	require.NoError(t, xcfg.Decode([]byte(raw), &function))

	decoded := function.AsFunction()
	require.Equal(t, "fn:decap", decoded.GetId().GetName())
	require.Len(t, decoded.GetChains(), 1)
	require.Equal(t, uint64(2), decoded.GetChains()[0].GetWeight())
	chain := decoded.GetChains()[0].GetChain()
	require.Equal(t, "default", chain.GetName())
	require.Len(t, chain.GetModules(), 1)
	require.Equal(t, "decap", chain.GetModules()[0].GetType())
	require.Equal(t, "decap0", chain.GetModules()[0].GetName())
}

// Test_FunctionConfig_RejectsOmittedChainWeight verifies that a chains
// entry without an explicit weight is refused.
func Test_FunctionConfig_RejectsOmittedChainWeight(t *testing.T) {
	raw := `
id:
  name: fn:decap
chains:
  - chain:
      name: default
      modules:
        - type: decap
          name: decap0
`
	var function operator.FunctionConfig
	err := xcfg.Decode([]byte(raw), &function)
	require.ErrorContains(t, err, "value must be set explicitly")
	require.ErrorContains(t, err, "weight")
}

// Test_FunctionConfig_AcceptsExplicitZeroWeight verifies that a spelled
// weight of zero still decodes, since zero deliberately disables a chain.
func Test_FunctionConfig_AcceptsExplicitZeroWeight(t *testing.T) {
	raw := `
id:
  name: fn:decap
chains:
  - chain:
      name: default
      modules:
        - type: decap
          name: decap0
    weight: 0
`
	var function operator.FunctionConfig
	require.NoError(t, xcfg.Decode([]byte(raw), &function))
	require.Equal(t, uint64(0), function.AsFunction().GetChains()[0].GetWeight())
}

// Test_Config_UnknownFunctionKeyIsCaught verifies that the known-keys walk
// sees inside a target's function, so a misspelled key there is reported.
func Test_Config_UnknownFunctionKeyIsCaught(t *testing.T) {
	raw := `
name: decap
targets:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
    function:
      id:
        name: fn:decap
      chain: []
`
	err := xcfg.CheckKnownKeys[operator.Config]([]byte(raw))
	require.ErrorContains(t, err, "chain")
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
