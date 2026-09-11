package decap

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// Option configures the DecapModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the decap module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// DecapModule is a control-plane component of a module that is responsible for
// decapsulating various kinds of tunnels.
type DecapModule struct {
	cfg          *Config
	attachment   *ffi.Attachment
	decapService *DecapService
}

func NewDecapModule(cfg *Config, options ...Option) (*DecapModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.decap.controlplane.decappb.v1.DecapService"))

	attachment, err := ffi.Attach(cfg.AttachConfig, "decap", log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	decapService := NewDecapService(NewBackend(agent))

	return &DecapModule{
		cfg:          cfg,
		attachment:   attachment,
		decapService: decapService,
	}, nil
}

func (m *DecapModule) Name() string {
	return "decap"
}

func (m *DecapModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *DecapModule) ServicesNames() []string {
	return []string{"modules.decap.controlplane.decappb.v1.DecapService"}
}

func (m *DecapModule) RegisterService(server *grpc.Server) {
	decappb.RegisterDecapServiceServer(server, m.decapService)
}

// Close closes the module.
func (m *DecapModule) Close() error {
	return m.attachment.Close()
}
