package operator

import (
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

const (
	DefaultReconcileInterval       = 30 * time.Second
	DefaultReconcileInitialBackoff = 500 * time.Millisecond
	DefaultReconcileMaxBackoff     = 30 * time.Second
	DefaultRegisterInterval        = 30 * time.Second
	DefaultRIBTTL                  = 5 * time.Minute
)

// Config is the top-level YAML configuration for yanet-route-operator.
type Config struct {
	Logging   logging.Config    `yaml:"logging"`
	Server    *GRPCServerConfig `yaml:"server"`
	Gateways  []GatewayConfig   `yaml:"gateways"`
	Register  RegisterConfig    `yaml:"register"`
	Reconcile ReconcileConfig   `yaml:"reconcile"`
	Static    StaticConfig      `yaml:"static"`
	// Function is the single gateway-side network function published by
	// this operator. Re-applied every reconcile pass; idempotent updates.
	Function       FunctionConfig       `yaml:"function"`
	LinkMap        map[string]string    `yaml:"link_map"`
	RIBTTL         time.Duration        `yaml:"rib_ttl"`
	NetlinkMonitor NetlinkMonitorConfig `yaml:"netlink_monitor"`
}

// GatewayConfig holds the name and gRPC endpoint of a single Gateway.
type GatewayConfig struct {
	// Name is a human-readable label used in logs and status reports.
	Name string `yaml:"name"`
	// Endpoint is the gRPC address of the Gateway.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// RegisterConfig holds the gateway registration heartbeat parameter.
type RegisterConfig struct {
	// Interval sets heartbeat period between registration refreshes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
}

// ReconcileConfig holds timing parameters for the reconcile loop.
type ReconcileConfig struct {
	// Interval is the steady-state period between successful reconcile
	// passes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
	// InitialBackoff is the first sleep after a failed pass; grows
	// exponentially.
	InitialBackoff xcfg.NonZero[time.Duration] `yaml:"initial_backoff"`
	// MaxBackoff caps the exponential backoff sleep.
	MaxBackoff xcfg.NonZero[time.Duration] `yaml:"max_backoff"`
}

func (m *ReconcileConfig) Validate() error {
	if m.MaxBackoff.Unwrap() < m.InitialBackoff.Unwrap() {
		return fmt.Errorf(
			"max_backoff (%s) must be >= initial_backoff (%s)",
			m.MaxBackoff.Unwrap(),
			m.InitialBackoff.Unwrap(),
		)
	}

	return nil
}

// FunctionConfig describes the single gateway-side function definition
// this operator publishes. The architectural invariant is "one operator
// manages exactly one network function with one chain and one module".
type FunctionConfig struct {
	// Name is the function identifier (e.g. "fn:route").
	Name xcfg.NonEmptyString `yaml:"name"`
	// Chain is the chain name (e.g. "default").
	Chain xcfg.NonEmptyString `yaml:"chain"`
	// Weight is the chain weight inside the function.
	Weight uint64 `yaml:"weight"`
	// Module is the route module config name this function targets
	// (e.g. "route0").
	Module xcfg.NonEmptyString `yaml:"module"`
}

// StaticConfig holds static RIB and neighbour seed data applied at
// startup before the operator begins serving requests.
type StaticConfig struct {
	Routes     []StaticRouteConfig     `yaml:"routes"`
	Neighbours []StaticNeighbourConfig `yaml:"neighbours"`
}

// StaticRouteConfig describes a single static-route entry to seed. The
// route is implicitly seeded into the operator's single managed module
// (Config.Function.Module).
type StaticRouteConfig struct {
	// Prefix is the destination prefix in CIDR notation.
	Prefix string `yaml:"prefix"`
	// NexthopAddr is the next-hop IP address.
	NexthopAddr string `yaml:"nexthop_addr"`
}

// StaticNeighbourConfig describes a single static neighbour entry to
// seed.
type StaticNeighbourConfig struct {
	// Table is the destination neighbour table name.
	Table string `yaml:"table"`
	// NextHop is the next-hop IP address.
	NextHop string `yaml:"next_hop"`
	// LinkAddr is the destination MAC address.
	LinkAddr string `yaml:"link_addr"`
	// HardwareAddr is the local interface MAC address.
	HardwareAddr string `yaml:"hardware_addr"`
	// Device is the egress interface name.
	Device string `yaml:"device"`
	// Priority overrides the table default priority when non-zero.
	Priority uint32 `yaml:"priority"`
}

// NetlinkMonitorConfig configures the kernel neighbour discovery via
// netlink.
type NetlinkMonitorConfig struct {
	// Disabled disables the netlink neighbour monitor entirely.
	Disabled bool `yaml:"disabled"`
	// TableName is the name of the kernel neighbour table.
	TableName string `yaml:"table_name"`
	// DefaultPriority is the default priority for kernel-learned
	// neighbour entries.
	DefaultPriority uint32 `yaml:"default_priority"`
}

func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}

	return nil
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		Server: &GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("localhost:50002"),
		},
		Reconcile: ReconcileConfig{
			Interval:       xcfg.MustNonZero(DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(DefaultReconcileMaxBackoff),
		},
		Register: RegisterConfig{
			Interval: xcfg.MustNonZero(DefaultRegisterInterval),
		},
		Function: FunctionConfig{
			Name:   xcfg.MustNonEmptyString("fn:route"),
			Chain:  xcfg.MustNonEmptyString("default"),
			Weight: 1,
			Module: xcfg.MustNonEmptyString("route0"),
		},
		RIBTTL:  DefaultRIBTTL,
		LinkMap: map[string]string{},
		NetlinkMonitor: NetlinkMonitorConfig{
			TableName:       "kernel",
			DefaultPriority: 100,
		},
	}
}

// LoadConfig reads a YAML file from path and returns the parsed Config.
//
// Default values are applied before unmarshalling so any absent field
// retains its default. Validation is driven by xcfg.Decode, which calls
// Validate() on every field whose type implements it.
func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := xcfg.Decode(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}
