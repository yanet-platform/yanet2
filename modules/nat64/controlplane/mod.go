package nat64

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

// Option configures the NAT64Module constructor.
type Option = modulebase.Option

// WithLog sets the logger for the nat64 module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// NAT64Module is a control-plane component responsible for NAT64 translation
type NAT64Module struct {
	*modulebase.Base
}

// NewNAT64Module creates a new NAT64 module instance
func NewNAT64Module(cfg *Config, options ...Option) (*NAT64Module, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	nat64Service := NewNAT64Service(NewBackend(base.Agent()), WithNAT64ServiceLog(base.Log()))
	base.AddService(nat64pb.NAT64Service_ServiceDesc.ServiceName, func(server *grpc.Server) {
		nat64pb.RegisterNAT64ServiceServer(server, nat64Service)
	})

	return &NAT64Module{Base: base}, nil
}
