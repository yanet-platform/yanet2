package acl

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

const (
	moduleType = "acl"
	agentName  = moduleType
)

// ModuleOption configures the ACLModule constructor.
type ModuleOption = modulebase.Option

// WithModuleLog sets the logger for the ACL module.
func WithModuleLog(log *zap.Logger) ModuleOption {
	return modulebase.WithLog(log)
}

// ACLModule is a control-plane component for ACL (Access Control List)
// module.
type ACLModule struct {
	*modulebase.Base

	aclService *ACLService
}

// NewACLModule creates a new ACL module instance.
func NewACLModule(cfg *Config, options ...ModuleOption) (*ACLModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	aclService := NewACLService(
		NewBackend(base.Agent()),
		WithLog(base.Log()),
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	)
	metricsService := NewMetricsService(aclService)
	base.AddService(aclpb.ACLService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		aclpb.RegisterACLServiceServer(server, aclService)
	})
	base.AddService(aclpb.MetricsService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		aclpb.RegisterMetricsServiceServer(server, metricsService)
	})

	return &ACLModule{Base: base, aclService: aclService}, nil
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this module.
func (m *ACLModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	var interceptors []grpc.UnaryServerInterceptor
	if si := m.aclService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	return interceptors
}

func (m *ACLModule) Close() error {
	// In-flight metric collections read the shared memory outside the
	// drained request handlers; wait for them before releasing it.
	m.aclService.DrainMetricsReads()
	return m.Base.Close()
}
