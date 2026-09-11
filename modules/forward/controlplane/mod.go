package forward

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

const (
	moduleType  = "forward"
	agentName   = moduleType
	serviceName = "modules.forward.controlplane.forwardpb.v1.ForwardService"
)

// Option configures the ForwardModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the forward module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// ForwardModule is a control-plane component of a module that is responsible for
// forwarding traffic between devices.
type ForwardModule struct {
	cfg            *Config
	attachment     *cpffi.Attachment
	forwardService *ForwardService
	metricsService *MetricsService
}

func NewForwardModule(cfg *Config, options ...Option) (*ForwardModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	attachment, err := cpffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	forwardService := NewForwardService(NewBackend(agent))
	metricsService := NewMetricsService(forwardService)

	return &ForwardModule{
		cfg:            cfg,
		attachment:     attachment,
		forwardService: forwardService,
		metricsService: metricsService,
	}, nil
}

func (m *ForwardModule) Name() string {
	return moduleType
}

func (m *ForwardModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *ForwardModule) ServicesNames() []string {
	return []string{serviceName, forwardpb.MetricsService_ServiceDesc.ServiceName}
}

func (m *ForwardModule) RegisterService(server *grpc.Server) {
	forwardpb.RegisterForwardServiceServer(server, m.forwardService)
	forwardpb.RegisterMetricsServiceServer(server, m.metricsService)
}

// Close closes the module.
func (m *ForwardModule) Close() error {
	return m.attachment.Close()
}
