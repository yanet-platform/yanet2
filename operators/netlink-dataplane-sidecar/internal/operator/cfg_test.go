package operator_test

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

const shippedConfigPath = "../../etc/yanet/yanet-netlink-dataplane-sidecar-default.yaml"

func TestConfigDefaults(t *testing.T) {
	cfg := sidecaroperator.DefaultConfig()
	require.Equal(t, zapcore.InfoLevel, cfg.Logging.Level)
	require.Equal(t, "[::1]:0", cfg.Server.Endpoint.Unwrap())
	require.Equal(t, commonoperator.DefaultRegisterInterval, cfg.Register.Interval.Unwrap())
	require.Equal(t, commonoperator.DefaultReconcileInterval, cfg.Reconcile.Interval.Unwrap())
	require.Equal(
		t,
		commonoperator.DefaultReconcileInitialBackoff,
		cfg.Reconcile.InitialBackoff.Unwrap(),
	)
	require.Equal(
		t,
		commonoperator.DefaultReconcileMaxBackoff,
		cfg.Reconcile.MaxBackoff.Unwrap(),
	)
	require.Equal(t, sidecaroperator.DefaultNetplanPath, cfg.NetplanPath.Unwrap())
	require.Equal(t, sidecaroperator.DefaultRouteTable, cfg.Route.Table)
	require.Equal(t, sidecaroperator.DefaultRouteProtocol, cfg.Route.Protocol)
	require.Equal(t, sidecaroperator.DefaultRoutePriority, cfg.Route.Priority)
	require.Equal(t, sidecaroperator.DefaultMaxRoutes, cfg.Route.MaxRoutes)
	require.Empty(t, cfg.LinkMap)

	require.NoError(t, xcfg.Decode([]byte(`
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
    neighbour_table: netlink-dataplane-numa0
`), cfg))
	require.Equal(
		t,
		uint32(sidecaroperator.DefaultNeighbourPriority),
		cfg.Gateways[0].NeighbourPriority,
	)
}

func TestGatewayConfigConvertsToCommonOperatorConfig(t *testing.T) {
	gateway := validGateway("numa0", "netlink-dataplane-numa0", nil)

	converted := gateway.OperatorConfig()

	require.Equal(t, gateway.Name, converted.Name)
	require.Equal(t, gateway.Endpoint, converted.Endpoint)
	require.Same(t, gateway.TLS, converted.TLS)
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*sidecaroperator.Config)
		errorContains string
	}{
		{
			name: "no gateways",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways = nil
			},
			errorContains: "at least one gateway",
		},
		{
			name: "empty gateway name",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Name = ""
			},
			errorContains: "name must be nonempty",
		},
		{
			name: "duplicate gateway name",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Devices = []string{"logical0"}
				cfg.Gateways = append(
					cfg.Gateways,
					validGateway("numa0", "netlink-dataplane-numa1", []string{"logical1"}),
				)
			},
			errorContains: `duplicate gateway name "numa0"`,
		},
		{
			name: "malformed server endpoint",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Server.Endpoint = xcfg.MustNonEmptyString("::1:8080")
			},
			errorContains: "invalid server endpoint",
		},
		{
			name: "malformed gateway endpoint",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Endpoint = xcfg.MustNonEmptyString("::1:8080")
			},
			errorContains: "invalid endpoint",
		},
		{
			name: "zero gateway port",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Endpoint = xcfg.MustNonEmptyString("[::1]:0")
			},
			errorContains: "invalid port",
		},
		{
			name: "missing neighbour table",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].NeighbourTable = ""
			},
			errorContains: "neighbour_table must be nonempty",
		},
		{
			name: "neighbour table outside owned namespace",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].NeighbourTable = "kernel-numa0"
			},
			errorContains: `outside reserved "netlink-dataplane-" namespace`,
		},
		{
			name: "duplicate neighbour table",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Devices = []string{"logical0"}
				cfg.Gateways = append(
					cfg.Gateways,
					validGateway("numa1", "netlink-dataplane-numa0", []string{"logical1"}),
				)
			},
			errorContains: `duplicate neighbour_table "netlink-dataplane-numa0"`,
		},
		{
			name: "zero neighbour priority",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].NeighbourPriority = 0
			},
			errorContains: "neighbour_priority must be positive",
		},
		{
			name: "multiple gateways require devices",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways = append(
					cfg.Gateways,
					validGateway("numa1", "netlink-dataplane-numa1", []string{"logical1"}),
				)
			},
			errorContains: "devices must be nonempty when multiple gateways",
		},
		{
			name: "duplicate device ownership",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Devices = []string{"logical0"}
				cfg.Gateways = append(
					cfg.Gateways,
					validGateway("numa1", "netlink-dataplane-numa1", []string{"logical0"}),
				)
			},
			errorContains: `device "logical0" is already owned`,
		},
		{
			name: "empty device",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Devices = []string{""}
			},
			errorContains: "device 0 must be nonempty",
		},
		{
			name: "duplicate logical link mapping",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.LinkMap = map[string]string{
					"kni0": "logical0",
					"kni1": "logical0",
				}
			},
			errorContains: `duplicate logical device "logical0"`,
		},
		{
			name: "mapped device without gateway owner",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Gateways[0].Devices = []string{"logical0"}
				cfg.Gateways = append(
					cfg.Gateways,
					validGateway("numa1", "netlink-dataplane-numa1", []string{"logical1"}),
				)
				cfg.LinkMap = map[string]string{"kni2": "logical2"}
			},
			errorContains: `logical device "logical2" without a gateway owner`,
		},
		{
			name: "invalid route table",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Route.Table = 0
			},
			errorContains: "table must be positive",
		},
		{
			name: "invalid route protocol",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Route.Protocol = 256
			},
			errorContains: "protocol must be within 1..255",
		},
		{
			name: "shared route protocol",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Route.Protocol = 186
			},
			errorContains: "reserved for a shared route origin",
		},
		{
			name: "invalid route priority",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Route.Priority = 0
			},
			errorContains: "priority must be positive",
		},
		{
			name: "invalid route limit",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Route.MaxRoutes = 0
			},
			errorContains: "max_routes must be positive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := sidecaroperator.DefaultConfig()
			cfg.Gateways = []sidecaroperator.GatewayConfig{
				validGateway("numa0", "netlink-dataplane-numa0", nil),
			}
			test.mutate(cfg)

			err := cfg.Validate()

			require.ErrorContains(t, err, test.errorContains)
		})
	}
}

func TestConfigRejectsRouteValuesThatOverflowNetlink(t *testing.T) {
	if ^uint(0) == uint(^uint32(0)) {
		t.Skip("int cannot represent values above uint32 on this platform")
	}
	overflowValue := uint64(^uint32(0)) + 1
	overflow := int(overflowValue)

	cfg := sidecaroperator.DefaultConfig()
	cfg.Gateways = []sidecaroperator.GatewayConfig{
		validGateway("numa0", "netlink-dataplane-numa0", nil),
	}
	cfg.Route.Table = overflow
	require.ErrorContains(t, cfg.Validate(), "table must be within")

	cfg.Route.Table = sidecaroperator.DefaultRouteTable
	cfg.Route.Priority = overflow
	require.ErrorContains(t, cfg.Validate(), "priority must be within")
}

func TestConfigAllowsIndependentEndpointFamiliesAndGRPCTargetURI(t *testing.T) {
	cfg := sidecaroperator.DefaultConfig()
	cfg.Server.Endpoint = xcfg.MustNonEmptyString("127.0.0.1:0")
	cfg.Server.AdvertiseEndpoint = "sidecar.internal:http"
	cfg.Gateways = []sidecaroperator.GatewayConfig{
		validGateway("numa0", "netlink-dataplane-numa0", nil),
	}
	cfg.Gateways[0].Endpoint = xcfg.MustNonEmptyString("dns:///gateway.internal:8080")

	require.NoError(t, cfg.Validate())
}

func TestConfigAllowsMultipleTablesOnOneGatewayEndpoint(t *testing.T) {
	cfg := sidecaroperator.DefaultConfig()
	first := validGateway("numa0", "netlink-dataplane-numa0", []string{"logical0"})
	second := validGateway("numa1", "netlink-dataplane-numa1", []string{"logical1"})
	second.Endpoint = first.Endpoint
	cfg.Gateways = []sidecaroperator.GatewayConfig{first, second}

	require.NoError(t, cfg.Validate())
}

func TestShippedDefaultConfig(t *testing.T) {
	data, err := os.ReadFile(shippedConfigPath)
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[sidecaroperator.Config](data))

	cfg, err := xcfg.LoadConfig[sidecaroperator.Config](shippedConfigPath)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Gateways)
	require.Equal(t, sidecaroperator.DefaultNetplanPath, cfg.NetplanPath.Unwrap())
}

func validGateway(
	name string,
	table string,
	devices []string,
) sidecaroperator.GatewayConfig {
	port := "8080"
	if name != "numa0" {
		port = "8081"
	}
	return sidecaroperator.GatewayConfig{
		Name:              name,
		Endpoint:          xcfg.MustNonEmptyString(net.JoinHostPort("::1", port)),
		NeighbourTable:    table,
		NeighbourPriority: sidecaroperator.DefaultNeighbourPriority,
		Devices:           devices,
	}
}
