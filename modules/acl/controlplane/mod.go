package acl

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

const (
	moduleType  = "acl"
	agentName   = moduleType
	serviceName = "modules.acl.controlplane.aclpb.v1.ACLService"
)

// ModuleOption configures the ACLModule constructor.
type ModuleOption func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithModuleLog sets the logger for the ACL module.
func WithModuleLog(log *zap.Logger) ModuleOption {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// ACLModule is a control-plane component for ACL (Access Control List)
// module.
type ACLModule struct {
	cfg            *Config
	attachment     *ffi.Attachment
	aclService     *ACLService
	metricsService *MetricsService
}

// NewACLModule creates a new ACL module instance.
func NewACLModule(cfg *Config, options ...ModuleOption) (*ACLModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	aclService := NewACLService(
		NewBackend(agent),
		WithLog(log),
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	)

	metricsService := NewMetricsService(aclService)

	return &ACLModule{
		cfg:            cfg,
		attachment:     attachment,
		aclService:     aclService,
		metricsService: metricsService,
	}, nil
}

func (m *ACLModule) Name() string {
	return moduleType
}

func (m *ACLModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *ACLModule) ServicesNames() []string {
	return []string{
		serviceName,
		aclpb.MetricsService_ServiceDesc.ServiceName,
	}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.aclService)
	aclpb.RegisterMetricsServiceServer(server, m.metricsService)
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
	return m.attachment.Close()
}
