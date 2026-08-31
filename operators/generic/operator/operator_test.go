package operator_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/generic/operator"

	_ "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
	_ "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// decodeConfig decodes raw over the built-in defaults.
func decodeConfig(t *testing.T, raw string) *operator.Config {
	t.Helper()
	cfg := operator.DefaultConfig()
	require.NoError(t, xcfg.Decode([]byte(raw), cfg))
	return cfg
}

// decapInstance spells a one-gateway decap instance pushing the module
// config at path, with a one-chain function referencing fnModule.
func decapInstance(path string, fnModule string) string {
	return fmt.Sprintf(`
name: decap
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: decap0
    method: %s
    file: %s
functions:
  - name: fn:decap
    chains:
      - chain:
          name: default
          modules:
            - type: decap
              name: %s
        weight: 1
`, decapUpdateMethod, path, fnModule)
}

// Test_NewOperator_BindsNameFromEntry verifies that a payload file without
// a name constructs, the entry's name bound into the request.
func Test_NewOperator_BindsNameFromEntry(t *testing.T) {
	path := writeModuleConfig(t, "prefixes6: [2001:db8::/32]\n")
	cfg := decodeConfig(t, decapInstance(path, "decap0"))

	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_RejectsMismatchedFileName verifies that a file naming
// another config than its entry is refused, not rebound.
func Test_NewOperator_RejectsMismatchedFileName(t *testing.T) {
	path := writeModuleConfig(t, "name: decap1\nprefixes6: [2001:db8::/32]\n")
	cfg := decodeConfig(t, decapInstance(path, "decap0"))

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `names config "decap1", but the entry is named "decap0"`)
}

// Test_NewOperator_RejectsUnpushedReference verifies that a function may
// not reference a config of a served type the instance does not push.
func Test_NewOperator_RejectsUnpushedReference(t *testing.T) {
	path := writeModuleConfig(t, "prefixes6: [2001:db8::/32]\n")
	cfg := decodeConfig(t, decapInstance(path, "decap1"))

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `function "fn:decap" references decap config "decap1"`)
	require.ErrorContains(t, err, "does not push")
}

// Test_NewOperator_AllowsForeignModuleTypes verifies that a chain may
// reference modules of types other operators own.
func Test_NewOperator_AllowsForeignModuleTypes(t *testing.T) {
	path := writeModuleConfig(t, "prefixes6: [2001:db8::/32]\n")
	raw := fmt.Sprintf(`
name: decap
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: decap0
    method: %s
    file: %s
functions:
  - name: fn:decap
    chains:
      - chain:
          name: default
          modules:
            - type: route
              name: route0
            - type: decap
              name: decap0
        weight: 1
`, decapUpdateMethod, path)
	cfg := decodeConfig(t, raw)

	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_ReadsModuleNameField verifies that a request naming
// its config through module_name, the route FIB spelling, works too.
func Test_NewOperator_ReadsModuleNameField(t *testing.T) {
	path := writeModuleConfig(t, "entries: []\n")
	raw := `
name: route
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: route0
    method: modules.route.controlplane.routepb.v1.RouteService/UpdateFIB
    file: ` + path + `
functions:
  - name: fn:route
    chains:
      - chain:
          name: default
          modules:
            - type: route
              name: %s
        weight: 1
`

	cfg := decodeConfig(t, fmt.Sprintf(raw, "route1"))
	_, err := operator.NewOperator(cfg)
	require.ErrorContains(t, err, `references route config "route1"`)

	cfg = decodeConfig(t, fmt.Sprintf(raw, "route0"))
	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_ReadsConfigNameField verifies that a request naming its
// config through config_name, the balancer spelling, binds and checks too.
func Test_NewOperator_ReadsConfigNameField(t *testing.T) {
	path := writeModuleConfig(t, "config_name: balancer1\n")
	raw := `
name: balancer
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: balancer0
    method: modules.balancer2.controlplane.balancerpb.v1.Balancer/UpdateConfig
    file: ` + path + `
`
	cfg := decodeConfig(t, raw)

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `names config "balancer1", but the entry is named "balancer0"`)
}
