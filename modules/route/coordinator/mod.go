package coordinator

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
)

// Module represents a route module in the coordinator.
type Module struct {
	configs map[uint32]*Config
	service *ModuleService
	log     *zap.SugaredLogger
}

// NewModule creates a new instance of the route module.
func NewModule(gatewayEndpoint string, log *zap.SugaredLogger) *Module {
	log = log.Named("route").With(zap.String("module", "route"))

	service := NewModuleService(gatewayEndpoint, log)

	return &Module{
		configs: map[uint32]*Config{},
		service: service,
		log:     log,
	}
}

func (m *Module) Name() string {
	return "route"
}

func (m *Module) RegisterService(server *grpc.Server) {
	coordinatorpb.RegisterModuleServiceServer(server, m.service)
}
