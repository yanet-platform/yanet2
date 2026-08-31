package operator_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/generic/operator"

	_ "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// decapTarget decodes a target pushing the module config at path with a
// one-chain function referencing module.
func decapTarget(t *testing.T, path string, module string) operator.TargetConfig {
	t.Helper()
	raw := fmt.Sprintf(`
name: decap0
method: %s
file: %s
function:
  id:
    name: fn:decap
  chains:
    - chain:
        name: default
        modules:
          - type: decap
            name: %s
      weight: 1
`, decapUpdateMethod, path, module)
	var target operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &target))
	return target
}

// testConfig returns a valid single-gateway config without targets.
func testConfig() *operator.Config {
	cfg := operator.DefaultConfig()
	cfg.Name = xcfg.MustNonEmptyString("decap")
	cfg.Gateways = []commonoperator.GatewayConfig{{
		Name:     "gw0",
		Endpoint: xcfg.MustNonEmptyString("[::1]:0"),
	}}
	return cfg
}

// Test_NewOperator_BuildsFromFiles verifies that a coherent target
// constructs an operator.
func Test_NewOperator_BuildsFromFiles(t *testing.T) {
	path := writeModuleConfig(t, "name: decap0\nprefixes6: [2001:db8::/32]\n")
	cfg := testConfig()
	cfg.Targets = []operator.TargetConfig{decapTarget(t, path, "decap0")}

	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_RejectsFunctionConfigMismatch verifies that a function
// referencing a config the file does not name is refused at construction.
func Test_NewOperator_RejectsFunctionConfigMismatch(t *testing.T) {
	path := writeModuleConfig(t, "name: decap0\nprefixes6: [2001:db8::/32]\n")
	cfg := testConfig()
	cfg.Targets = []operator.TargetConfig{decapTarget(t, path, "decap1")}

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `function "fn:decap" does not reference config "decap0"`)
}

// Test_NewOperator_RejectsNamelessModuleConfig verifies that a file naming
// no config is refused instead of being pushed with an empty name.
func Test_NewOperator_RejectsNamelessModuleConfig(t *testing.T) {
	path := writeModuleConfig(t, "prefixes6: [2001:db8::/32]\n")
	cfg := testConfig()
	cfg.Targets = []operator.TargetConfig{decapTarget(t, path, "decap0")}

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, "names no config")
}

// Test_NewOperator_RejectsModuleTypeMismatch verifies that referencing the
// pushed config's name under another module type is refused.
func Test_NewOperator_RejectsModuleTypeMismatch(t *testing.T) {
	path := writeModuleConfig(t, "name: decap0\nprefixes6: [2001:db8::/32]\n")
	raw := fmt.Sprintf(`
name: decap0
method: %s
file: %s
function:
  id:
    name: fn:decap
  chains:
    - chain:
        name: default
        modules:
          - type: forward
            name: decap0
      weight: 1
`, decapUpdateMethod, path)
	var target operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &target))
	cfg := testConfig()
	cfg.Targets = []operator.TargetConfig{target}

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `function "fn:decap" does not reference config "decap0"`)
}

// Test_NewOperator_ReadsModuleNameField verifies that a request naming its
// config through module_name, the route FIB spelling, is checked too.
func Test_NewOperator_ReadsModuleNameField(t *testing.T) {
	path := writeModuleConfig(t, "module_name: route0\n")
	raw := fmt.Sprintf(`
name: route0
method: modules.route.controlplane.routepb.v1.RouteService/UpdateFIB
file: %s
function:
  id:
    name: fn:route
  chains:
    - chain:
        name: default
        modules:
          - type: route
            name: %%s
      weight: 1
`, path)

	var mismatched operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(raw, "route1")), &mismatched))
	cfg := testConfig()
	cfg.Targets = []operator.TargetConfig{mismatched}
	_, err := operator.NewOperator(cfg)
	require.ErrorContains(t, err, `does not reference config "route0"`)

	var matched operator.TargetConfig
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(raw, "route0")), &matched))
	cfg = testConfig()
	cfg.Targets = []operator.TargetConfig{matched}
	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}
