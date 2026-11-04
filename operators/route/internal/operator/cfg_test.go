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

// Test_Config_LoadRejectsEmptyLinkMapDevice verifies that an explicit empty
// mapped device fails configuration loading with a field-labelled error.
func Test_Config_LoadRejectsEmptyLinkMapDevice(t *testing.T) {
	data := fmt.Sprintf(`gateways:
  - name: numa0
    endpoint: "[::1]:8080"
link_map:
  eth0: %q
`, "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	_, err := xcfg.LoadConfig[operator.Config](path)
	require.EqualError(t, err, `failed to parse config file: link_map["eth0"] is required`)
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

// Test_Config_IncludedGatewayMTLS verifies that mounted transport settings
// retain the complete client identity and main-file listener overrides.
func Test_Config_IncludedGatewayMTLS(t *testing.T) {
	directory := t.TempDir()
	transport := filepath.Join(directory, "transport")
	require.NoError(t, os.Mkdir(transport, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(transport, "client.yaml"), []byte(`gateways:
  - name: secure
    endpoint: gateway.example:8443
    tls:
      ca_file: /pki/ca.pem
      server_name: gateway.example
      key_pairs:
        - type: file
          config:
            cert_file: /pki/client.pem
            key_file: /pki/client-key.pem
server:
  advertise_endpoint: operator.example:9443
`), 0o600))
	path := filepath.Join(directory, "main.yaml")
	require.NoError(t, os.WriteFile(path, []byte("include: [transport]\nserver:\n  endpoint: '[::1]:9443'\n"), 0o600))
	config, err := xcfg.LoadConfig[operator.Config](path, xcfg.WithKnownFields())
	require.NoError(t, err)
	require.Equal(t, "[::1]:9443", config.Server.Endpoint.Unwrap())
	require.Equal(t, "operator.example:9443", config.Server.AdvertiseEndpoint)
	require.Len(t, config.Gateways, 1)
	gateway := config.Gateways[0]
	require.Equal(t, "gateway.example:8443", gateway.Endpoint.Unwrap())
	require.NotNil(t, gateway.TLS)
	require.Equal(t, "/pki/ca.pem", gateway.TLS.CAFile)
	require.Equal(t, "gateway.example", gateway.TLS.ServerName)
	require.Len(t, gateway.TLS.KeyPairs, 1)
	require.Equal(t, "file", gateway.TLS.KeyPairs[0].Type)
	var keyPair struct {
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
	}
	require.NoError(t, gateway.TLS.KeyPairs[0].Config.Decode(&keyPair))
	require.Equal(t, "/pki/client.pem", keyPair.CertFile)
	require.Equal(t, "/pki/client-key.pem", keyPair.KeyFile)
}
