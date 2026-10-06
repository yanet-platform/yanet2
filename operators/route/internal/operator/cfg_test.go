package operator_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in operator.Config.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-route-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[operator.Config](data))
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies
// that the shipped file inherits the loopback port-zero listener endpoint.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[operator.Config](
		"../../etc/yanet/yanet-route-operator-default.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
}

// Test_Config_LoadRejectsInvalidStaticNeighbourDevice verifies that invalid
// static egress devices fail configuration loading with indexed field errors.
func Test_Config_LoadRejectsInvalidStaticNeighbourDevice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		device  string
		message string
	}{
		{
			name:    "empty device",
			device:  "",
			message: "failed to parse config file: static.neighbours[0]: device is required",
		},
		{
			name:    "device with embedded NUL",
			device:  "eth\x00backup",
			message: "failed to parse config file: static.neighbours[0]: device must not contain NUL",
		},
		{
			name:    "device at buffer length",
			device:  strings.Repeat("a", commonpb.MaxDeviceNameLen),
			message: "failed to parse config file: static.neighbours[0]: device must be shorter than 80 bytes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := fmt.Sprintf(
				`gateways:
  - name: numa0
    endpoint: "[::1]:8080"
static:
  neighbours:
    - next_hop: 203.0.113.1
      link_addr: 02:00:00:00:00:01
      hardware_addr: 02:00:00:00:00:02
      device: %q
`,
				tc.device,
			)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
			_, err := xcfg.LoadConfig[operator.Config](path)
			require.EqualError(t, err, tc.message)
		})
	}
}
