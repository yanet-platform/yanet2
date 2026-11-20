// Package example implements the control plane of the reference
// out-of-tree module.
//
// It is a deliberate mirror of modules/blackhole's control plane, showing
// the minimal shape of a module service: an ffi.Attach-based module, a
// configstore-backed gRPC CRUD service, and the CGO binding for the
// module's api/ archive. See docs/module-sdk.md for the full picture.
package example

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	examplepb "github.com/yanet-platform/yanet2/sdk/example/controlplane/examplepb/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	moduleName = "example"
	agentName  = moduleName
)

// Option configures the ExampleModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the example module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// ExampleModule is the control-plane component of the example module.
type ExampleModule struct {
	cfg            *Config
	attachment     *ffi.Attachment
	exampleService *ExampleService
}

// NewExampleModule creates a new ExampleModule.
func NewExampleModule(cfg *Config, options ...Option) (*ExampleModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", examplepb.ExampleService_ServiceDesc.ServiceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	exampleService := NewExampleService(NewBackend(agent))

	return &ExampleModule{
		cfg:            cfg,
		attachment:     attachment,
		exampleService: exampleService,
	}, nil
}

// Name returns the module name.
func (m *ExampleModule) Name() string {
	return moduleName
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *ExampleModule) ServicesNames() []string {
	return []string{examplepb.ExampleService_ServiceDesc.ServiceName}
}

// RegisterService registers the example module's gRPC service.
func (m *ExampleModule) RegisterService(server *grpc.Server) {
	examplepb.RegisterExampleServiceServer(server, m.exampleService)
}

// Close releases shared memory resources held by the module.
func (m *ExampleModule) Close() error {
	return m.attachment.Close()
}
