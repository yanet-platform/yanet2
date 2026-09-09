package operator_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

const shippedConfigPath = "../../etc/yanet/yanet-netlink-dataplane-sidecar-default.yaml"

// Test_Config_PublicationDefaults verifies that transport alternatives share
// one table identity and a configurable positive publication deadline.
func Test_Config_PublicationDefaults(t *testing.T) {
	config := twoGatewayConfig()
	require.Equal(t, "netlink-dataplane-default", config.NeighbourTable)
	require.Equal(t, uint32(100), config.NeighbourPriority)
	require.Equal(t, 5*time.Second, config.NeighbourPublishTimeout)
	require.NoError(t, xcfg.Decode([]byte("neighbour_publish_timeout: 15s\n"), config))
	require.Equal(t, 15*time.Second, config.PublicationConfig().Timeout)
	config.Gateways[1].Endpoint = config.Gateways[0].Endpoint
	require.NoError(t, config.Validate())
}

// Test_Config_InvalidFields verifies that malformed publication, transport and
// scheduling configuration is rejected before resources can be allocated.
func Test_Config_InvalidFields(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*sidecaroperator.Config)
	}{
		{name: "no gateways", mutate: func(config *sidecaroperator.Config) { config.Gateways = nil }},
		{name: "empty gateway name", mutate: func(config *sidecaroperator.Config) { config.Gateways[0].Name = "" }},
		{name: "duplicate gateway name", mutate: func(config *sidecaroperator.Config) { config.Gateways[1].Name = config.Gateways[0].Name }},
		{name: "malformed listener", mutate: func(config *sidecaroperator.Config) { config.Server.Endpoint = xcfg.MustNonEmptyString("::1:8080") }},
		{name: "zero gateway port", mutate: func(config *sidecaroperator.Config) { config.Gateways[0].Endpoint = xcfg.MustNonEmptyString("[::1]:0") }},
		{name: "missing table", mutate: func(config *sidecaroperator.Config) { config.NeighbourTable = "" }},
		{name: "foreign table", mutate: func(config *sidecaroperator.Config) { config.NeighbourTable = "static" }},
		{name: "zero priority", mutate: func(config *sidecaroperator.Config) { config.NeighbourPriority = 0 }},
		{name: "zero timeout", mutate: func(config *sidecaroperator.Config) { config.NeighbourPublishTimeout = 0 }},
		{name: "negative timeout", mutate: func(config *sidecaroperator.Config) { config.NeighbourPublishTimeout = -time.Second }},
		{name: "invalid link name", mutate: func(config *sidecaroperator.Config) { config.LinkMap = map[string]string{"../kni0": "logical0"} }},
		{name: "zero registration interval", mutate: func(config *sidecaroperator.Config) { config.Register.Interval = xcfg.NonZero[time.Duration]{} }},
		{name: "zero reconcile interval", mutate: func(config *sidecaroperator.Config) { config.Reconcile.Interval = xcfg.NonZero[time.Duration]{} }},
		{name: "zero initial backoff", mutate: func(config *sidecaroperator.Config) { config.Reconcile.InitialBackoff = xcfg.NonZero[time.Duration]{} }},
		{name: "zero max backoff", mutate: func(config *sidecaroperator.Config) { config.Reconcile.MaxBackoff = xcfg.NonZero[time.Duration]{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := twoGatewayConfig()
			test.mutate(config)
			require.Error(t, config.Validate())
		})
	}
}

// Test_Config_NegativeScheduling verifies that YAML duration decoding cannot
// admit negative heartbeat, reconciliation or retry delays.
func Test_Config_NegativeScheduling(t *testing.T) {
	for _, field := range []string{"register:\n  interval", "reconcile:\n  interval", "reconcile:\n  initial_backoff", "reconcile:\n  max_backoff", "neighbour_publish_timeout"} {
		t.Run(field, func(t *testing.T) {
			require.Error(t, xcfg.Decode([]byte(field+": -1s\n"), twoGatewayConfig()))
		})
	}
}

// Test_Config_IndependentEndpoints verifies that a numeric listener, advertised
// service name and DNS target can use independent address forms.
func Test_Config_IndependentEndpoints(t *testing.T) {
	config := twoGatewayConfig()
	config.Server.Endpoint = xcfg.MustNonEmptyString("127.0.0.1:0")
	config.Server.AdvertiseEndpoint = "sidecar.internal:http"
	config.Gateways[0].Endpoint = xcfg.MustNonEmptyString("dns:///gateway.internal:8080")
	require.NoError(t, config.Validate())
}

// Test_Config_ShippedDefaults verifies that the installed YAML contains only
// supported fields and loads a complete single-source transport configuration.
func Test_Config_ShippedDefaults(t *testing.T) {
	data, err := os.ReadFile(shippedConfigPath)
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[sidecaroperator.Config](data))
	config, err := xcfg.LoadConfig[sidecaroperator.Config](shippedConfigPath)
	require.NoError(t, err)
	require.NotEmpty(t, config.Gateways)
	require.Equal(t, sidecaroperator.DefaultNetplanPath, config.NetplanPath.Unwrap())
}
