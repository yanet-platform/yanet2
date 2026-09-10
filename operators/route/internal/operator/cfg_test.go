package operator_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
)

// Test_Config_DeviceNameBoundary verifies that static input and remote ownership
// reject device names that would lose identity at the dataplane ABI boundary.
func Test_Config_DeviceNameBoundary(t *testing.T) {
	for _, input := range []string{"static", "remote"} {
		for _, test := range []struct {
			name   string
			device string
			valid  bool
		}{
			{name: "79 bytes", device: strings.Repeat("d", 79), valid: true},
			{name: "80 bytes", device: strings.Repeat("d", 80)},
			{name: "embedded NUL", device: "logical0\x00other"},
		} {
			t.Run(input+"/"+test.name, func(t *testing.T) {
				config := operator.DefaultConfig()
				config.Gateways = []commonoperator.GatewayConfig{{Name: "first"}}
				if input == "static" {
					config.Static.Neighbours = []operator.StaticNeighbourConfig{{Device: test.device}}
				} else {
					config.NetlinkMonitor.Disabled = true
					config.Readiness.RemoteNeighbourTable = "remote"
					config.GatewayDevices = map[string][]string{"first": {test.device}}
				}
				if test.valid {
					require.NoError(t, config.Validate())
				} else {
					require.Error(t, config.Validate())
				}
			})
		}
	}
}

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
