package coordinator

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/coordinator/coordinatorpb"
)

// Module represents a forward module in the coordinator.
type Module struct {
	configs map[uint32]*Config
	service *ModuleService
	log     *zap.SugaredLogger
}

// NewModule creates a new instance of the forward module.
func NewModule(gatewayEndpoint string, log *zap.SugaredLogger) *Module {
	log = log.Named("forward").With(zap.String("module", "forward"))

	service := NewModuleService(gatewayEndpoint, log)

	return &Module{
		configs: map[uint32]*Config{},
		service: service,
		log:     log,
	}
}

func (m *Module) Name() string {
	return "forward"
}

func (m *Module) RegisterService(server *grpc.Server) {
	coordinatorpb.RegisterModuleServiceServer(server, m.service)
}
