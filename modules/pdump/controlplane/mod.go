package pdump

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	cpdump "github.com/yanet-platform/yanet2/modules/pdump/bindings/go/cpdump"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
)

// Option configures the PdumpModule constructor.
type Option = modulebase.Option

// WithLog sets the logger for the pdump module.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// PdumpModule is a control-plane component of a packet dump module.
//
// It also hosts the ring service. That service owns the standalone named
// rings created through this agent.
type PdumpModule struct {
	*modulebase.Base

	cfg         *Config
	service     *PdumpService
	ringService *ring.RingService
	log         *zap.Logger
}

func NewPdumpModule(cfg *Config, options ...Option) (*PdumpModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	// The C library's log callback writes to the bindings package's
	// logger, so it must be set before any C call that can log.
	cpdump.SetLogger(base.Log())
	cpdump.SetDebugEBPF(cfg.DebugEBPF)

	ringService := ring.NewRingService(base.Agent(), ring.WithLog(base.Log()))

	service := NewPdumpService(
		NewBackend(base.Agent(), WithBackendLog(base.Log())),
		newRingOwner(ringService),
		WithPdumpServiceLog(base.Log()),
	)
	base.AddService(pdumppb.PdumpService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		pdumppb.RegisterPdumpServiceServer(server, service)
	})
	base.AddService(ring.ServiceName, func(server *grpc.Server) {
		ringService.Register(server)
	})

	return &PdumpModule{
		Base:        base,
		cfg:         cfg,
		service:     service,
		ringService: ringService,
		log:         base.Log(),
	}, nil
}

// Run runs the module until the specified context is canceled.
// Implements the gateway.BackgroundService interface.
func (m *PdumpModule) Run(ctx context.Context) error {
	<-ctx.Done()
	m.log.Info("closing pdump service")
	m.service.Shutdown()
	return nil
}
