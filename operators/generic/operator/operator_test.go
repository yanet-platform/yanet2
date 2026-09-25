package operator_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/generic/operator"

	_ "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// decodeConfig decodes the document over the built-in defaults.
func decodeConfig(t *testing.T, raw string) *operator.Config {
	t.Helper()
	cfg := operator.DefaultConfig()
	require.NoError(t, xcfg.Decode([]byte(raw), cfg))
	return cfg
}

// decapInstance spells a one-gateway decap instance pushing one module
// config, with a one-chain function referencing it.
func decapInstance(path string) string {
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
              name: decap0
        weight: 1
`, decapUpdateMethod, path)
}

// Test_NewOperator_BindsNameFromEntry verifies that a payload file without
// a name constructs, the entry's name bound into the request.
func Test_NewOperator_BindsNameFromEntry(t *testing.T) {
	path := writeModuleConfig(t, "prefixes6: [2001:db8::/32]\n")
	cfg := decodeConfig(t, decapInstance(path))

	runnable, err := operator.NewOperator(cfg)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_RejectsMismatchedFileName verifies that a file naming
// another config than its entry is refused, not rebound.
func Test_NewOperator_RejectsMismatchedFileName(t *testing.T) {
	path := writeModuleConfig(t, "name: decap1\nprefixes6: [2001:db8::/32]\n")
	cfg := decodeConfig(t, decapInstance(path))

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `names config "decap1", but the entry is named "decap0"`)
}

// Test_NewOperator_ReadsModuleNameField verifies that a request naming
// its config through module_name, the route FIB spelling, binds too.
func Test_NewOperator_ReadsModuleNameField(t *testing.T) {
	path := writeModuleConfig(t, "module_name: route1\n")
	raw := `
name: route
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: route0
    method: modules.route.controlplane.routepb.v1.RouteService/UpdateFIB
    file: ` + path + `
`
	cfg := decodeConfig(t, raw)

	_, err := operator.NewOperator(cfg)

	require.ErrorContains(t, err, `names config "route1", but the entry is named "route0"`)
}

// Test_NewOperator_ReadsConfigNameField verifies that an explicit config name
// must match the entry's name for construction to succeed.
func Test_NewOperator_ReadsConfigNameField(t *testing.T) {
	cases := []struct {
		name       string
		configName string
		wantErr    string
	}{
		{
			name:       "mismatched config name is rejected",
			configName: "fixture1",
			wantErr:    `names config "fixture1", but the entry is named "fixture0"`,
		},
		{
			name:       "matching config name is accepted",
			configName: "fixture0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeModuleConfig(t, "config_name: "+tc.configName+"\n")
			raw := fmt.Sprintf(`
name: fixture
gateways:
  - name: gw0
    endpoint: "[::1]:0"
configs:
  - name: fixture0
    method: %s
    file: %s
`, configNameMethod, path,
			)
			cfg := decodeConfig(t, raw)

			runnable, err := operator.NewOperator(cfg)
			if runnable != nil {
				t.Cleanup(func() { require.NoError(t, runnable.Close()) })
			}
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
