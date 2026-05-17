package operator

import (
	"errors"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

const (
	DefaultRIBTTL = 5 * time.Minute
)

// Config is the top-level YAML configuration for yanet-route-operator.
type Config struct {
	Logging   logging.Config             `yaml:"logging"`
	Server    *operator.GRPCServerConfig `yaml:"server"`
	Gateways  []operator.GatewayConfig   `yaml:"gateways"`
	Register  operator.RegisterConfig    `yaml:"register"`
	Reconcile operator.ReconcileConfig   `yaml:"reconcile"`
	Static    StaticConfig               `yaml:"static"`
	// Function is the single gateway-side network function published by
	// this operator. Re-applied every reconcile pass; idempotent updates.
	Function       FunctionConfig       `yaml:"function"`
	LinkMap        map[string]string    `yaml:"link_map"`
	RIBTTL         time.Duration        `yaml:"rib_ttl"`
	NetlinkMonitor NetlinkMonitorConfig `yaml:"netlink_monitor"`
}

func (m *Config) Default() {
	*m = *DefaultConfig()
}

// LoggingConfig exposes the embedded logging configuration to the
// generic operator CLI helper.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
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
		Server: &operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("localhost:50002"),
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		Register: operator.RegisterConfig{
			Interval: xcfg.MustNonZero(operator.DefaultRegisterInterval),
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

// FunctionConfig describes the single gateway-side function definition
// this operator publishes.
//
// The architectural invariant is "one operator manages exactly one network
// function with one chain and one module".
//
// TODO: ^^^ - this is not true anymore :(
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
	// IgnorePdump skips function updates when the existing "default" chain
	// already matches Modules once every pdump:* module is filtered out.
	IgnorePdump bool `yaml:"ignore_pdump"`
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
