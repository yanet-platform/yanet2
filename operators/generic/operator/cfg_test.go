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
// configs against unknown keys, including inside a function entry.
func Test_ShippedInstanceConfigs_NoUnknownKeys(t *testing.T) {
	for _, path := range shippedInstanceConfigs {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
		})
	}
}

// Test_ShippedInstanceConfigs_ConstructEndToEnd verifies that every
// shipped instance config decodes and constructs an operator.
//
// Construction loads each payload file and decodes it against the
// spelled method, so the shipped payloads are covered too.
func Test_ShippedInstanceConfigs_ConstructEndToEnd(t *testing.T) {
	for _, path := range shippedInstanceConfigs {
		t.Run(path, func(t *testing.T) {
			cfg, err := xcfg.LoadConfig[operator.Config](path)
			require.NoError(t, err)
			require.NotEmpty(t, cfg.Configs)
			require.NotEmpty(t, cfg.Functions)

			// The shipped files reference install paths, which the
			// repository keeps under etc/yanet.
			for idx := range cfg.Configs {
				cfg.Configs[idx].File = xcfg.MustNonEmptyString(strings.Replace(
					cfg.Configs[idx].File.Unwrap(), "/etc/yanet2/", "../etc/yanet/", 1,
				))
			}

			runnable, err := operator.NewOperator(cfg)
			require.NoError(t, err)
			require.NoError(t, runnable.Close())
		})
	}
}

// Test_FunctionConfig_IgnorePdump_DefaultsTrue verifies that an omitted
// ignore_pdump key defaults to true.
func Test_FunctionConfig_IgnorePdump_DefaultsTrue(t *testing.T) {
	raw := `
name: fn:decap
chains:
  - chain:
      name: default
      modules:
        - type: decap
          name: decap0
    weight: 1
`
	var function operator.FunctionConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &function))
	require.True(t, function.IgnorePdump)
}

// Test_FunctionConfig_IgnorePdump_ExplicitFalse verifies that an explicit
// ignore_pdump false is preserved.
func Test_FunctionConfig_IgnorePdump_ExplicitFalse(t *testing.T) {
	raw := `
name: fn:decap
ignore_pdump: false
chains:
  - chain:
      name: default
      modules:
        - type: decap
          name: decap0
    weight: 1
`
	var function operator.FunctionConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &function))
	require.False(t, function.IgnorePdump)
}

// Test_FunctionConfig_ConvertsToProto verifies that a spelled function
// converts into the whole ynpb.Function.
func Test_FunctionConfig_ConvertsToProto(t *testing.T) {
	raw := `
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
// sees inside a function entry, so a misspelled key there is reported.
func Test_Config_UnknownFunctionKeyIsCaught(t *testing.T) {
	raw := `
name: decap
configs:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
functions:
  - name: fn:decap
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
configs:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
`
	cfg := operator.DefaultConfig()
	err := xcfg.Decode([]byte(raw), cfg)
	require.ErrorContains(t, err, `duplicate gateway name "numa0"`)
}

// Test_Config_RejectsDuplicateConfigEntry verifies that pushing the same
// config through the same method twice is refused.
//
// The two entries spell the method with and without the leading slash,
// which the transport treats as one RPC.
func Test_Config_RejectsDuplicateConfigEntry(t *testing.T) {
	raw := `
name: decap
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
configs:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
  - name: decap0
    method: /modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/other.yaml
`
	cfg := operator.DefaultConfig()
	err := xcfg.Decode([]byte(raw), cfg)
	require.ErrorContains(t, err, `config "decap0" is pushed twice`)
}

// Test_Config_RejectsDuplicateFunctions verifies that two functions
// sharing a name are refused at decode time.
func Test_Config_RejectsDuplicateFunctions(t *testing.T) {
	raw := `
name: decap
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
configs:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
functions:
  - name: fn:decap
    chains:
      - chain:
          name: default
          modules:
            - type: decap
              name: decap0
        weight: 1
  - name: fn:decap
    chains:
      - chain:
          name: other
          modules:
            - type: decap
              name: decap0
        weight: 1
`
	cfg := operator.DefaultConfig()
	err := xcfg.Decode([]byte(raw), cfg)
	require.ErrorContains(t, err, `function "fn:decap" is declared twice`)
}
