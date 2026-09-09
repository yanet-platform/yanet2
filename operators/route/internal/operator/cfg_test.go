package operator_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
)

// Test_ShippedDefaultConfig_NoUnknownKeys verifies that shipped settings match
// the operator's supported configuration surface.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-route-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies that
// ordinary default configuration needs no fixed operator listener port.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[operator.Config]("../../etc/yanet/yanet-route-operator-default.yaml")
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
}
