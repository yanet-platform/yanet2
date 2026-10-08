// Package example implements the control plane of the reference
// out-of-tree module.
//
// It is a deliberate mirror of modules/blackhole's control plane, showing
// the minimal shape of a module service: an ffi.Attach-based module, a
// configstore-backed gRPC CRUD service, and the CGO binding for the
// module's api/ archive. See docs/module-sdk.md for the full picture.
package example_rs

import (
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	examplerspb "github.com/yanet-platform/yanet2/sdk/example-rs/controlplane/examplerspb/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	moduleName = "example_rs"
	agentName  = moduleName
)

// Option configures the ExampleRsModule constructor.
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

// ExampleRsModule is the control-plane component of the example module.
type ExampleRsModule struct {
	cfg            *Config
	attachment     *ffi.Attachment
	exampleService *ExampleRsService
}

// NewExampleRsModule creates a new ExampleRsModule.
func NewExampleRsModule(cfg *Config, options ...Option) (*ExampleRsModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", examplerspb.ExampleRsService_ServiceDesc.ServiceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	exampleService := NewExampleRsService(NewBackend(agent))

	return &ExampleRsModule{
		cfg:            cfg,
		attachment:     attachment,
		exampleService: exampleService,
	}, nil
}

// Name returns the module name.
func (m *ExampleRsModule) Name() string {
	return moduleName
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *ExampleRsModule) ServicesNames() []string {
	return []string{examplerspb.ExampleRsService_ServiceDesc.ServiceName}
}

// RegisterService registers the example module's gRPC service.
func (m *ExampleRsModule) RegisterService(server *grpc.Server) {
	examplerspb.RegisterExampleRsServiceServer(server, m.exampleService)
}

// Close releases shared memory resources held by the module.
func (m *ExampleRsModule) Close() error {
	return m.attachment.Close()
}
