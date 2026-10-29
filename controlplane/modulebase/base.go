// Package modulebase carries the skeleton every built-in module and
// device control plane shares: the attachment to the shared-memory
// agent, the gateway service surface and the close.
//
// A module embeds a Base, registers its gRPC services in its
// constructor and keeps its extras — interceptors, background work,
// extra close steps — beside it.
package modulebase

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Option configures the Base constructor.
type Option func(*options)

// options holds the optional parameters of the Base constructor.
type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger of the base.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// registeredService is one gRPC service of a module: its
// fully-qualified name and its registration on a server.
type registeredService struct {
	name     string
	register func(*grpc.Server)
}

// Name returns the fully-qualified service name.
func (m registeredService) Name() string {
	return m.name
}

// Register registers the service on the server.
func (m registeredService) Register(server *grpc.Server) {
	m.register(server)
}

// Base is the shared skeleton of a built-in control plane: the
// attachment to the shared-memory agent, the gateway service methods
// and the close.
//
// Services are added from the module constructor alone, before the
// gateway reads the surface, so nothing guards the list.
type Base struct {
	attachment *ffi.Attachment
	name       string
	endpoint   string
	services   []registeredService

	log *zap.Logger
}

// NewBase attaches to the shared-memory agent under the module's type
// name.
//
// The returned base carries a logger scoped to the module.
func NewBase(cfg ffi.AttachConfig, name, endpoint string, options ...Option) (*Base, error) {
	opts := newOptions()
	for _, option := range options {
		option(opts)
	}

	log := opts.Log.With(zap.String("module", name))

	attachment, err := ffi.Attach(cfg, name, log)
	if err != nil {
		return nil, err
	}

	return &Base{
		attachment: attachment,
		name:       name,
		endpoint:   endpoint,
		log:        log,
	}, nil
}

// Agent returns the shared-memory agent the module publishes through.
func (m *Base) Agent() *ffi.Agent {
	return m.attachment.Agent
}

// Log returns the logger scoped to the module.
func (m *Base) Log() *zap.Logger {
	return m.log
}

// AddService registers one gRPC service of the module.
//
// It is called from the module constructor only.
func (m *Base) AddService(name string, register func(*grpc.Server)) {
	m.services = append(m.services, registeredService{name: name, register: register})
}

// Name returns the module's shared-memory type name.
func (m *Base) Name() string {
	return m.name
}

// Endpoint returns the address the module's services bind when run in a
// separate process. Inside the gateway process it is ignored.
func (m *Base) Endpoint() string {
	return m.endpoint
}

// ServicesNames returns the fully-qualified names of the module's gRPC
// services.
func (m *Base) ServicesNames() []string {
	names := make([]string, 0, len(m.services))
	for _, service := range m.services {
		names = append(names, service.Name())
	}

	return names
}

// RegisterService registers every service of the module on the server.
func (m *Base) RegisterService(server *grpc.Server) {
	for _, service := range m.services {
		service.Register(server)
	}
}

// Close releases the shared-memory attachment.
func (m *Base) Close() error {
	return m.attachment.Close()
}
