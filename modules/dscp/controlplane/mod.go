package dscp

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Option configures the DscpModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the dscp module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// DscpModule is a control-plane component of a module that is responsible for
// DSCP marking of packets.
type DscpModule struct {
	*modulebase.Base
}

func NewDSCPModule(cfg *Config, options ...Option) (*DscpModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	dscpService := NewDscpService(newBackend(base.Agent()))
	base.AddService(dscppb.DscpService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		dscppb.RegisterDscpServiceServer(server, dscpService)
	})

	return &DscpModule{Base: base}, nil
}
