package operator

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/logging"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

const (
	DefaultNetplanPath       = "/etc/netplan/00-interfaces.yaml"
	DefaultRouteTable        = 254
	DefaultRouteProtocol     = 242
	DefaultRoutePriority     = 1
	DefaultMaxRoutes         = 1_000_000
	DefaultNeighbourPriority = 100
)

// Config is the top-level YAML configuration for the netlink dataplane
// sidecar.
type Config struct {
	Logging     logging.Config                  `yaml:"logging"`
	Server      commonoperator.GRPCServerConfig `yaml:"server"`
	Gateways    []GatewayConfig                 `yaml:"gateways"`
	Register    commonoperator.RegisterConfig   `yaml:"register"`
	Reconcile   commonoperator.ReconcileConfig  `yaml:"reconcile"`
	NetplanPath xcfg.NonEmptyString             `yaml:"netplan_path"`
	LinkMap     map[string]string               `yaml:"link_map"`
	Route       RouteConfig                     `yaml:"route"`
}

// GatewayConfig describes one gateway and the neighbour state it owns.
type GatewayConfig struct {
	Name              string                 `yaml:"name"`
	Endpoint          xcfg.NonEmptyString    `yaml:"endpoint"`
	TLS               *xgrpc.ClientTLSConfig `yaml:"tls"`
	NeighbourTable    string                 `yaml:"neighbour_table"`
	NeighbourPriority uint32                 `yaml:"neighbour_priority"`
	Devices           []string               `yaml:"devices"`
}

// RouteConfig controls Linux route ownership and the stream size limit.
type RouteConfig struct {
	Table     int `yaml:"table"`
	Protocol  int `yaml:"protocol"`
	Priority  int `yaml:"priority"`
	MaxRoutes int `yaml:"max_routes"`
}

// Default resets the receiver before YAML decoding.
func (m *Config) Default() {
	*m = *DefaultConfig()
}

// LoggingConfig exposes logging configuration to the common CLI helper.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
}

// Validate checks gateway ownership and sidecar-specific invariants.
func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}
	if err := validateListenEndpoint(m.Server.Endpoint.Unwrap()); err != nil {
		return fmt.Errorf("invalid server endpoint: %w", err)
	}
	if err := m.Server.Validate(); err != nil {
		return fmt.Errorf("invalid server configuration: %w", err)
	}
	if m.Register.Interval.Unwrap() <= 0 {
		return errors.New("register.interval must be positive")
	}
	if m.Reconcile.Interval.Unwrap() <= 0 {
		return errors.New("reconcile.interval must be positive")
	}
	if m.Reconcile.InitialBackoff.Unwrap() <= 0 {
		return errors.New("reconcile.initial_backoff must be positive")
	}
	if m.Reconcile.MaxBackoff.Unwrap() <= 0 {
		return errors.New("reconcile.max_backoff must be positive")
	}
	if err := m.Reconcile.Validate(); err != nil {
		return fmt.Errorf("invalid reconcile configuration: %w", err)
	}
	if m.NetplanPath.Unwrap() == "" {
		return errors.New("netplan_path must be nonempty")
	}
	if err := m.Route.Validate(); err != nil {
		return fmt.Errorf("invalid route configuration: %w", err)
	}

	names := make(map[string]int, len(m.Gateways))
	tables := make(map[string]int, len(m.Gateways))
	deviceOwners := map[string]string{}
	for idx, gateway := range m.Gateways {
		if strings.TrimSpace(gateway.Name) == "" {
			return fmt.Errorf("gateway %d: name must be nonempty", idx)
		}
		if previous, duplicate := names[gateway.Name]; duplicate {
			return fmt.Errorf(
				"gateway %d: duplicate gateway name %q (already used by gateway %d)",
				idx,
				gateway.Name,
				previous,
			)
		}
		names[gateway.Name] = idx

		if gateway.Endpoint.Unwrap() == "" {
			return fmt.Errorf("gateway %q: endpoint must be nonempty", gateway.Name)
		}
		if err := validateGRPCTarget(gateway.Endpoint.Unwrap()); err != nil {
			return fmt.Errorf("gateway %q: invalid endpoint: %w", gateway.Name, err)
		}
		if gateway.TLS != nil {
			if err := gateway.TLS.Validate(); err != nil {
				return fmt.Errorf("gateway %q: invalid TLS configuration: %w", gateway.Name, err)
			}
		}
		if strings.TrimSpace(gateway.NeighbourTable) == "" {
			return fmt.Errorf("gateway %q: neighbour_table must be nonempty", gateway.Name)
		}
		if err := neighbour.ValidateTableName(gateway.NeighbourTable); err != nil {
			return fmt.Errorf("gateway %q: invalid neighbour_table: %w", gateway.Name, err)
		}
		if previous, duplicate := tables[gateway.NeighbourTable]; duplicate {
			return fmt.Errorf(
				"gateway %q: duplicate neighbour_table %q (already used by gateway %d)",
				gateway.Name,
				gateway.NeighbourTable,
				previous,
			)
		}
		tables[gateway.NeighbourTable] = idx
		if gateway.NeighbourPriority == 0 {
			return fmt.Errorf("gateway %q: neighbour_priority must be positive", gateway.Name)
		}
		if len(m.Gateways) > 1 && len(gateway.Devices) == 0 {
			return fmt.Errorf(
				"gateway %q: devices must be nonempty when multiple gateways are configured",
				gateway.Name,
			)
		}

		for deviceIdx, device := range gateway.Devices {
			if strings.TrimSpace(device) == "" {
				return fmt.Errorf("gateway %q: device %d must be nonempty", gateway.Name, deviceIdx)
			}
			if owner, duplicate := deviceOwners[device]; duplicate {
				return fmt.Errorf(
					"gateway %q: device %q is already owned by gateway %q",
					gateway.Name,
					device,
					owner,
				)
			}
			deviceOwners[device] = gateway.Name
		}
	}

	logicalLinks := make(map[string]string, len(m.LinkMap))
	for osName, logicalName := range m.LinkMap {
		if strings.TrimSpace(osName) == "" {
			return errors.New("link_map contains an empty OS link name")
		}
		if strings.TrimSpace(logicalName) == "" {
			return fmt.Errorf("link_map entry %q has an empty logical device name", osName)
		}
		if previous, duplicate := logicalLinks[logicalName]; duplicate {
			return fmt.Errorf(
				"link_map entries %q and %q map to duplicate logical device %q",
				previous,
				osName,
				logicalName,
			)
		}
		logicalLinks[logicalName] = osName
		if len(m.Gateways) > 1 {
			if _, owned := deviceOwners[logicalName]; !owned {
				return fmt.Errorf(
					"link_map entry %q maps to logical device %q without a gateway owner",
					osName,
					logicalName,
				)
			}
		}
	}

	return nil
}

func validateListenEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return err
	}
	if strings.TrimSpace(host) != host {
		return errors.New("host contains surrounding whitespace")
	}
	if port == "0" {
		return nil
	}
	portNumber, err := net.LookupPort("tcp", port)
	if err != nil || portNumber <= 0 {
		return fmt.Errorf("invalid port %q", port)
	}
	return nil
}

func validateGRPCTarget(target string) error {
	if strings.TrimSpace(target) != target || target == "" {
		return errors.New("target is empty or contains surrounding whitespace")
	}
	if _, port, err := net.SplitHostPort(target); err == nil {
		portNumber, lookupErr := net.LookupPort("tcp", port)
		if lookupErr != nil || portNumber <= 0 {
			return fmt.Errorf("invalid port %q", port)
		}
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return err
	}
	if parsed.Scheme != "" && parsed.Host == "" && parsed.Path == "" && parsed.Opaque == "" {
		return errors.New("target URI has no endpoint")
	}
	return nil
}

// Validate checks route ownership and resource-limit values.
func (m *RouteConfig) Validate() error {
	if m.Table <= 0 {
		return fmt.Errorf("table must be positive, got %d", m.Table)
	}
	if uint64(m.Table) > route.MaxKernelRouteValue {
		return fmt.Errorf("table must be within 1..%d, got %d", route.MaxKernelRouteValue, m.Table)
	}
	if m.Protocol < 1 || m.Protocol > 255 {
		return fmt.Errorf("protocol must be within 1..255, got %d", m.Protocol)
	}
	if route.IsSharedRouteProtocol(m.Protocol) {
		return fmt.Errorf("protocol %d is reserved for a shared route origin", m.Protocol)
	}
	if m.Priority <= 0 {
		return fmt.Errorf("priority must be positive, got %d", m.Priority)
	}
	if uint64(m.Priority) > route.MaxKernelRouteValue {
		return fmt.Errorf("priority must be within 1..%d, got %d", route.MaxKernelRouteValue, m.Priority)
	}
	if m.MaxRoutes <= 0 {
		return fmt.Errorf("max_routes must be positive, got %d", m.MaxRoutes)
	}
	return nil
}

// DefaultConfig returns the sidecar's production defaults.
func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		Server: commonoperator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("[::1]:0"),
		},
		Register: commonoperator.RegisterConfig{
			Interval: xcfg.MustNonZero(commonoperator.DefaultRegisterInterval),
		},
		Reconcile: commonoperator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(commonoperator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(commonoperator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(commonoperator.DefaultReconcileMaxBackoff),
		},
		NetplanPath: xcfg.MustNonEmptyString(DefaultNetplanPath),
		LinkMap:     map[string]string{},
		Route: RouteConfig{
			Table:     DefaultRouteTable,
			Protocol:  DefaultRouteProtocol,
			Priority:  DefaultRoutePriority,
			MaxRoutes: DefaultMaxRoutes,
		},
	}
}

// OperatorConfig converts a sidecar gateway into the common dialing and
// registration shape.
func (m GatewayConfig) OperatorConfig() commonoperator.GatewayConfig {
	return commonoperator.GatewayConfig{
		Name:     m.Name,
		Endpoint: m.Endpoint,
		TLS:      m.TLS,
	}
}

// OperatorGateways converts all configured gateways without sharing the
// backing slice.
func (m *Config) OperatorGateways() []commonoperator.GatewayConfig {
	gateways := make([]commonoperator.GatewayConfig, len(m.Gateways))
	for idx, gateway := range m.Gateways {
		gateways[idx] = gateway.OperatorConfig()
	}
	return gateways
}

// UnmarshalYAML applies per-gateway defaults before decoding explicit values.
func (m *GatewayConfig) UnmarshalYAML(node *yaml.Node) error {
	type plainGatewayConfig GatewayConfig
	decoded := plainGatewayConfig{
		NeighbourPriority: DefaultNeighbourPriority,
	}
	if err := node.Decode(&decoded); err != nil {
		return fmt.Errorf("decode gateway: %w", err)
	}
	*m = GatewayConfig(decoded)
	return nil
}
