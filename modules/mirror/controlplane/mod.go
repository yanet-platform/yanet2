package mirror

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

const agentName = "mirror"

// MirrorModule is a control-plane component of a module that is responsible for
// mirroring traffic between devices.
type MirrorModule struct {
	cfg           *Config
	shm           *cpffi.SharedMemory
	agent         *cpffi.Agent
	mirrorService *MirrorService
	log           *zap.Logger
}

func NewMirrorModule(cfg *Config, log *zap.Logger) (*MirrorModule, error) {
	log = log.With(zap.String("module", "modules.mirror.controlplane.mirrorpb.v1.MirrorService"))

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

	mirrorService := NewMirrorService(NewBackend(agent))

	return &MirrorModule{
		cfg:           cfg,
		shm:           shm,
		agent:         agent,
		mirrorService: mirrorService,
		log:           log,
	}, nil
}

func (m *MirrorModule) Name() string {
	return agentName
}

func (m *MirrorModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *MirrorModule) ServicesNames() []string {
	return []string{"modules.mirror.controlplane.mirrorpb.v1.MirrorService"}
}

func (m *MirrorModule) RegisterService(server *grpc.Server) {
	mirrorpb.RegisterMirrorServiceServer(server, m.mirrorService)
}

// Close closes the module.
func (m *MirrorModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
