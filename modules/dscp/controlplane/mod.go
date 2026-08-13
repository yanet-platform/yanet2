package dscp

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Option configures the DscpModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the dscp module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// DscpModule is a control-plane component of a module that is responsible for
// DSCP marking of packets.
type DscpModule struct {
	cfg         *Config
	shm         *ffi.SharedMemory
	agent       *ffi.Agent
	dscpService *DscpService
}

func NewDSCPModule(cfg *Config, options ...Option) (*DscpModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "dscp"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug(
		"mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("dscp", cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	dscpService := NewDscpService(newBackend(agent))

	return &DscpModule{
		cfg:         cfg,
		shm:         shm,
		agent:       agent,
		dscpService: dscpService,
	}, nil
}

func (m *DscpModule) Name() string {
	return "dscp"
}

func (m *DscpModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *DscpModule) ServicesNames() []string {
	return []string{"modules.dscp.controlplane.dscppb.v1.DscpService"}
}

func (m *DscpModule) RegisterService(server *grpc.Server) {
	dscppb.RegisterDscpServiceServer(server, m.dscpService)
}

// Close closes the module.
func (m *DscpModule) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
