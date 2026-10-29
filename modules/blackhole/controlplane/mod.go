// Package blackhole implements Blackhole module.
package blackhole

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

const (
	moduleName = "blackhole"
	agentName  = moduleName
)

// Option configures the BlackholeModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the blackhole module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// BlackholeModule is a controlplane component for blackhole module.
type BlackholeModule struct {
	*modulebase.Base
}

// NewBlackholeModule creates a new BlackholeModule.
func NewBlackholeModule(cfg *Config, options ...Option) (*BlackholeModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	blackholeService := NewBlackholeService(NewBackend(base.Agent()))
	base.AddService(blackholepb.BlackholeService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		blackholepb.RegisterBlackholeServiceServer(server, blackholeService)
	})

	return &BlackholeModule{Base: base}, nil
}
