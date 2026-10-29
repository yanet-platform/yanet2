package unrdup

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// Option configures the UnrdupModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the unrdup module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// UnrdupModule is the control-plane component of the unrdup module.
type UnrdupModule struct {
	*modulebase.Base
}

func NewUnrdupModule(cfg *Config, options ...Option) (*UnrdupModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	unrdupService := NewUnrdupService(newBackend(base.Agent()))
	base.AddService(unrduppb.UnrdupService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		unrduppb.RegisterUnrdupServiceServer(server, unrdupService)
	})

	return &UnrdupModule{Base: base}, nil
}
