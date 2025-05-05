package registry

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
)

// RegistryService implements the Registry gRPC service.
type RegistryService struct {
	coordinatorpb.UnimplementedRegistryServiceServer
	registry   *Registry
	log        *zap.SugaredLogger
	mu         sync.RWMutex
	moduleInfo map[string]*module
}

// NewRegistryService creates a new registry service.
func NewRegistryService(registry *Registry, log *zap.SugaredLogger) *RegistryService {
	return &RegistryService{
		registry:   registry,
		log:        log.Named("registry"),
		moduleInfo: make(map[string]*module),
	}
}

// RegisterModule registers a new module in the coordinator.
func (m *RegistryService) RegisterModule(
	ctx context.Context,
	req *coordinatorpb.RegisterModuleRequest,
) (*coordinatorpb.RegisterModuleResponse, error) {
	name := req.GetName()
	endpoint := req.GetEndpoint()

	m.log.Infow("registering module",
		zap.String("name", name),
		zap.String("endpoint", endpoint),
	)

	// Connect to the module endpoint
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to connect to module endpoint: %v", err)
	}

	// Create a module struct that implements Module interface
	mod := &module{
		name:     name,
		endpoint: endpoint,
		conn:     coordinatorpb.NewModuleServiceClient(conn),
	}

	m.mu.Lock()
	m.moduleInfo[name] = mod
	m.mu.Unlock()

	m.registry.RegisterModule(name, mod)

	return &coordinatorpb.RegisterModuleResponse{}, nil
}

// ListModules lists all registered modules.
func (m *RegistryService) ListModules(
	ctx context.Context,
	req *coordinatorpb.ListModulesRequest,
) (*coordinatorpb.ListModulesResponse, error) {
	modules := m.registry.ListModules()

	return &coordinatorpb.ListModulesResponse{
		Modules: modules,
	}, nil
}

// GetModule gets a specific module by name.
func (m *RegistryService) GetModule(
	ctx context.Context,
	req *coordinatorpb.GetModuleRequest,
) (*coordinatorpb.GetModuleResponse, error) {
	m.mu.RLock()
	info, ok := m.moduleInfo[req.GetName()]
	m.mu.RUnlock()

	if !ok {
		return &coordinatorpb.GetModuleResponse{
			Exists: false,
		}, nil
	}

	return &coordinatorpb.GetModuleResponse{
		Exists:   true,
		Name:     info.name,
		Endpoint: info.endpoint,
	}, nil
}

// module is a concrete implementation of the Module interface.
type module struct {
	name     string
	endpoint string
	conn     coordinatorpb.ModuleServiceClient
}

func (m *module) SetupConfig(ctx context.Context, numaIdx uint32, configName string, config []byte) error {
	_, err := m.conn.SetupConfig(ctx, &coordinatorpb.SetupConfigRequest{
		NumaNode:   numaIdx,
		ConfigName: configName,
		Config:     config,
	})
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	return nil
}
