package forward

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

const (
	agentName   = "forward"
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
	shm            *cpffi.SharedMemory
	agent          *cpffi.Agent
	forwardService *ForwardService
	metricsService *MetricsService
	log            *zap.Logger
}

func NewForwardModule(cfg *Config, options ...Option) (*ForwardModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	shm, err := cpffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	forwardService := NewForwardService(NewBackend(agent))
	metricsService := NewMetricsService(forwardService)

	return &ForwardModule{
		cfg:            cfg,
		shm:            shm,
		agent:          agent,
		forwardService: forwardService,
		metricsService: metricsService,
		log:            log,
	}, nil
}

func (m *ForwardModule) Name() string {
	return agentName
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
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
