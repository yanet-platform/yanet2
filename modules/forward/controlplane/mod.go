package forward

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

const (
	moduleType = "forward"
	agentName  = moduleType
)

// Option configures the ForwardModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the forward module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// ForwardModule is a control-plane component of a module that is responsible for
// forwarding traffic between devices.
type ForwardModule struct {
	*modulebase.Base
}

func NewForwardModule(cfg *Config, options ...Option) (*ForwardModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	forwardService := NewForwardService(NewBackend(base.Agent()))
	metricsService := NewMetricsService(forwardService)
	base.AddService(forwardpb.ForwardService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		forwardpb.RegisterForwardServiceServer(server, forwardService)
	})
	base.AddService(forwardpb.MetricsService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		forwardpb.RegisterMetricsServiceServer(server, metricsService)
	})

	return &ForwardModule{Base: base}, nil
}
