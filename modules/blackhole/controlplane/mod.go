// Package blackhole implements Blackhole module.
package blackhole

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	moduleName  = "blackhole"
	agentName   = moduleName
	serviceName = "modules.blackhole.controlplane.blackholepb.v1.BlackholeService"
)

// Option configures the BlackholeModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the blackhole module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// BlackholeModule is a controlplane component for blackhole module.
type BlackholeModule struct {
	cfg              *Config
	attachment       *ffi.Attachment
	blackholeService *BlackholeService
}

// NewBlackholeModule creates a new BlackholeModule.
func NewBlackholeModule(cfg *Config, options ...Option) (*BlackholeModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	blackholeService := NewBlackholeService(NewBackend(agent))

	return &BlackholeModule{
		cfg:              cfg,
		attachment:       attachment,
		blackholeService: blackholeService,
	}, nil
}

// Name returns the module name.
func (m *BlackholeModule) Name() string {
	return moduleName
}

// Endpoint returns the gRPC endpoint for the blackhole module.
func (m *BlackholeModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *BlackholeModule) ServicesNames() []string {
	return []string{serviceName}
}

// RegisterService registers the blackhole module's gRPC service.
func (m *BlackholeModule) RegisterService(server *grpc.Server) {
	blackholepb.RegisterBlackholeServiceServer(server, m.blackholeService)
}

// Close releases shared memory resources held by the module.
func (m *BlackholeModule) Close() error {
	return m.attachment.Close()
}
