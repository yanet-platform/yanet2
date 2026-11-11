package operator_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/pipeline/internal/operator"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in operator.Config.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-pipeline-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies
// that the shipped file inherits the loopback port-zero listener endpoint.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[operator.Config](
		"../../etc/yanet/yanet-pipeline-operator-default.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
}

// Test_Config_RejectsDuplicateGateways verifies that two gateways sharing a
// name are refused at decode time.
func Test_Config_RejectsDuplicateGateways(t *testing.T) {
	raw := `
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
  - name: numa0
    endpoint: "[::1]:8082"
`
	cfg := operator.DefaultConfig()
	err := xcfg.Decode([]byte(raw), cfg)
	require.ErrorContains(t, err, `duplicate gateway name "numa0"`)
}
