package decap

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// Option configures the DecapModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the decap module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// DecapModule is a control-plane component of a module that is responsible for
// decapsulating various kinds of tunnels.
type DecapModule struct {
	*modulebase.Base
}

func NewDecapModule(cfg *Config, options ...Option) (*DecapModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	decapService := NewDecapService(NewBackend(base.Agent()))
	base.AddService(decappb.DecapService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		decappb.RegisterDecapServiceServer(server, decapService)
	})

	return &DecapModule{Base: base}, nil
}
