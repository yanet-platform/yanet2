package dscp

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb"
)

// DscpModule is a control-plane component of a module that is responsible for
// DSCP marking of packets.
type DscpModule struct {
	cfg         *Config
	shm         *ffi.SharedMemory
	agent       *ffi.Agent
	dscpService *DscpService
	log         *zap.SugaredLogger
}

func NewDSCPModule(cfg *Config, log *zap.SugaredLogger) (*DscpModule, error) {
	log = log.With(zap.String("module", "dscppb.DscpService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("dscp", cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	dscpService := NewDscpService(agent, log)

	return &DscpModule{
		cfg:         cfg,
		shm:         shm,
		agent:       agent,
		dscpService: dscpService,
		log:         log,
	}, nil
}

func (m *DscpModule) Name() string {
	return "dscp"
}

func (m *DscpModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *DscpModule) ServicesNames() []string {
	return []string{"dscppb.DscpService"}
}

func (m *DscpModule) RegisterService(server *grpc.Server) {
	dscppb.RegisterDscpServiceServer(server, m.dscpService)
}

// Close closes the module.
func (m *DscpModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
