package operator_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// gatewayCLIConfig exposes named transports through the command contract.
type gatewayCLIConfig struct {
	Gateways []operator.GatewayConfig `yaml:"gateways"`
}

func (m *gatewayCLIConfig) GatewayConfigs() *[]operator.GatewayConfig {
	return &m.Gateways
}

// writeGatewayCLIConfig returns a host config with two distinct NUMA transports.
func writeGatewayCLIConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
gateways:
  - name: numa0
    endpoint: "[::1]:9000"
  - name: numa1
    endpoint: "[::1]:9001"
    tls:
      ca_file: /etc/yanet2/ca.pem
      server_name: gateway.example
`), 0600))
	return path
}

// Test_RunOperator_GatewayEnvironment verifies that the factory receives only
// active named transports, with new endpoints and preserved TLS settings.
func Test_RunOperator_GatewayEnvironment(t *testing.T) {
	t.Setenv("YANET_KUBERNETES_GATEWAYS", `[{"name":"numa1","endpoint":"gateway-numa1.test.svc:8080"}]`)
	path := writeGatewayCLIConfig(t)
	stop := errors.New("factory stopped before opening connections")
	var observed []operator.GatewayConfig

	err := operator.RunOperator(path, func(config *gatewayCLIConfig, log *zap.Logger) (operator.Runnable, error) {
		observed = config.Gateways
		return nil, stop
	})

	require.ErrorIs(t, err, stop)
	require.Equal(t, []operator.GatewayConfig{{
		Name:     "numa1",
		Endpoint: xcfg.MustNonEmptyString("gateway-numa1.test.svc:8080"),
		TLS: &xgrpc.ClientTLSConfig{
			CAFile:     "/etc/yanet2/ca.pem",
			ServerName: "gateway.example",
		},
	}}, observed)
}

// Test_RunOperator_InvalidGatewayEnvironment verifies that invalid deployment
// input prevents construction, before any transport can open a connection.
func Test_RunOperator_InvalidGatewayEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "empty environment value", value: ""},
		{name: "malformed JSON", value: "["},
		{name: "empty active set", value: "[]"},
		{name: "null active set", value: "null"},
		{name: "unknown gateway", value: `[{"name":"numa2","endpoint":"gateway:8080"}]`},
		{name: "duplicate selection", value: `[{"name":"numa1","endpoint":"gateway:8080"},{"name":"numa1","endpoint":"other:8080"}]`},
		{name: "missing endpoint", value: `[{"name":"numa1"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YANET_KUBERNETES_GATEWAYS", tc.value)
			constructed := false
			err := operator.RunOperator(writeGatewayCLIConfig(t), func(config *gatewayCLIConfig, log *zap.Logger) (operator.Runnable, error) {
				constructed = true
				return nil, errors.New("unexpected factory invocation")
			})
			require.ErrorContains(t, err, "YANET_KUBERNETES_GATEWAYS")
			require.False(t, constructed)
		})
	}
}

// Test_RunOperator_WithoutGatewayEnvironment verifies that deployments without
// an override retain every configured transport and its original address.
func Test_RunOperator_WithoutGatewayEnvironment(t *testing.T) {
	t.Setenv("YANET_KUBERNETES_GATEWAYS", "")
	require.NoError(t, os.Unsetenv("YANET_KUBERNETES_GATEWAYS"))
	stop := errors.New("factory stopped")
	var observed []operator.GatewayConfig
	err := operator.RunOperator(writeGatewayCLIConfig(t), func(config *gatewayCLIConfig, log *zap.Logger) (operator.Runnable, error) {
		observed = config.Gateways
		return nil, stop
	})
	require.ErrorIs(t, err, stop)
	require.Len(t, observed, 2)
	require.Equal(t, "[::1]:9000", observed[0].Endpoint.Unwrap())
	require.Equal(t, "[::1]:9001", observed[1].Endpoint.Unwrap())
}

// Test_RunOperator_UnsupportedConfigIgnoresGatewayEnvironment verifies that
// the optional deployment contract does not affect other command consumers.
func Test_RunOperator_UnsupportedConfigIgnoresGatewayEnvironment(t *testing.T) {
	t.Setenv("YANET_KUBERNETES_GATEWAYS", "not-json")
	type unmodifiedConfig gatewayCLIConfig
	stop := errors.New("factory stopped")
	var observed []operator.GatewayConfig
	err := operator.RunOperator(writeGatewayCLIConfig(t), func(config *unmodifiedConfig, log *zap.Logger) (operator.Runnable, error) {
		observed = config.Gateways
		return nil, stop
	})
	require.ErrorIs(t, err, stop)
	require.Len(t, observed, 2)
	require.Equal(t, "[::1]:9000", observed[0].Endpoint.Unwrap())
	require.Equal(t, "[::1]:9001", observed[1].Endpoint.Unwrap())
}
