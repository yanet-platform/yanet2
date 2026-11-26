package nat64s

import (
	"context"
	"github.com/c2h5oh/datasize"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
)

const (
	moduleType = "nat64s"
	agentName  = moduleType
)

// Option configures the NAT64sModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the nat64s module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// NAT64sModule is the control-plane component of the stateful NAT64
// module.
type NAT64sModule struct {
	*modulebase.Base

	cfg  *Config
	maps *MapRegistry
}

func NewNAT64sModule(cfg *Config, options ...Option) (*NAT64sModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	maps := NewMapRegistry(base.Agent(), WithRegistryLog(base.Log()))

	backend := NewBackend(base.Agent(), maps)
	service := NewNAT64sService(backend, WithServiceLog(base.Log()))
	base.AddService(nat64spb.NAT64SService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		nat64spb.RegisterNAT64SServiceServer(server, service)
	})

	return &NAT64sModule{
		Base: base,
		cfg:  cfg,
		maps: maps,
	}, nil
}

// Run reclaims stale session layers until the context is cancelled.
//
// The sweep publishes config generations through the module's agent, so it
// belongs to the phase that ends before anything is closed rather than to
// the module's own construction.
func (m *NAT64sModule) Run(ctx context.Context) error {
	m.maps.RunSweeper(ctx, m.cfg.StaleLayerSweepInterval)
	return nil
}

// Config is the nat64s module configuration.
type Config struct {
	modulebase.BaseConfig `yaml:",inline"`

	// StaleLayerSweepInterval is the period between stale-layer sweeps of
	// the session tables.
	StaleLayerSweepInterval time.Duration `yaml:"stale_layer_sweep_interval"`
}

func DefaultConfig() *Config {
	return &Config{
		// The nat64state map objects this module creates allocate in
		// its agent zone, so the default must hold at least the
		// default map dimensions: a zero-sizing CreateMap picks a
		// 1,048,576-entry index per side of the object, and two such
		// layers need well over 100 MB before module configs and
		// allocator overhead.
		BaseConfig:              modulebase.NewBaseConfig(1024 * datasize.MB),
		StaleLayerSweepInterval: DefaultStaleLayerSweepInterval,
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
