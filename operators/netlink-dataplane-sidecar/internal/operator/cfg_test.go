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
		{name: "negative registration interval", mutate: func(config *sidecaroperator.Config) { config.Register.Interval = xcfg.MustNonZero(-time.Second) }},
		{name: "negative reconcile interval", mutate: func(config *sidecaroperator.Config) { config.Reconcile.Interval = xcfg.MustNonZero(-time.Second) }},
		{name: "negative initial backoff", mutate: func(config *sidecaroperator.Config) { config.Reconcile.InitialBackoff = xcfg.MustNonZero(-time.Second) }},
		{name: "negative max backoff", mutate: func(config *sidecaroperator.Config) { config.Reconcile.MaxBackoff = xcfg.MustNonZero(-time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := twoGatewayConfig()
			test.mutate(config)
			require.Error(t, config.Validate())
		})
	}
}

// Test_Config_NegativeScheduling verifies that YAML decoding invokes semantic
// scheduling validation rather than accepting a syntactically valid duration.
func Test_Config_NegativeScheduling(t *testing.T) {
	require.Error(t, xcfg.Decode([]byte("reconcile:\n  interval: -1s\n"), twoGatewayConfig()))
}

// Test_Config_ResolverEndpoints verifies that resolver URIs contain a usable
// endpoint before lazy gRPC client construction can defer their failure.
func Test_Config_ResolverEndpoints(t *testing.T) {
	for _, test := range []struct {
		name, target string
		valid        bool
	}{
		{name: "empty DNS endpoint", target: "dns:///"},
		{name: "DNS authority without endpoint", target: "dns://resolver.internal"},
		{name: "DNS authority with empty path", target: "dns://resolver.internal/"},
		{name: "DNS zero port", target: "dns:///gateway.internal:0"},
		{name: "passthrough authority without endpoint", target: "passthrough://gateway.internal:8080"},
		{name: "passthrough zero port", target: "passthrough:///[::1]:0"},
		{name: "DNS hostname and port", target: "dns:///gateway.internal:8080", valid: true},
		{name: "DNS default port", target: "dns:///gateway.internal", valid: true},
		{name: "DNS authority and endpoint", target: "dns://127.0.0.1:53/gateway.internal:8080", valid: true},
		{name: "passthrough IPv6 endpoint", target: "passthrough:///[::1]:8080", valid: true},
		{name: "Unix socket", target: "unix:///run/yanet.sock", valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := twoGatewayConfig()
			config.Gateways[0].Endpoint = xcfg.MustNonEmptyString(test.target)
			if test.valid {
				require.NoError(t, config.Validate())
			} else {
				require.Error(t, config.Validate())
			}
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
