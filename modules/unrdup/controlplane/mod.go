package unrdup

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// Option configures the UnrdupModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the unrdup module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// UnrdupModule is the control-plane component of the unrdup module.
type UnrdupModule struct {
	cfg           *Config
	attachment    *ffi.Attachment
	unrdupService *UnrdupService
}

func NewUnrdupModule(cfg *Config, options ...Option) (*UnrdupModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "unrdup"))

	attachment, err := ffi.Attach(cfg.AttachConfig, "unrdup", log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	return &UnrdupModule{
		cfg:           cfg,
		attachment:    attachment,
		unrdupService: NewUnrdupService(newBackend(agent)),
	}, nil
}

func (m *UnrdupModule) Name() string {
	return "unrdup"
}

func (m *UnrdupModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *UnrdupModule) ServicesNames() []string {
	return []string{"modules.unrdup.controlplane.unrduppb.v1.UnrdupService"}
}

func (m *UnrdupModule) RegisterService(server *grpc.Server) {
	unrduppb.RegisterUnrdupServiceServer(server, m.unrdupService)
}

// Close closes the module.
func (m *UnrdupModule) Close() error {
	return m.attachment.Close()
}
