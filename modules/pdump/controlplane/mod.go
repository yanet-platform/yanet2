package pdump

import (
	"context"
	"errors"
	"fmt"

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

	service *PdumpService
	log     *zap.Logger
}

// NewPdumpModule discards predecessor capture state before serving requests.
//
// The previous control plane must have stopped and startup must be the exclusive
// writer of the target names. Completed removals are not rolled back on error.
func NewPdumpModule(cfg *Config, options ...Option) (*PdumpModule, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, moduleType, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	// The C library's log callback writes to the bindings package's
	// logger, so it must be set before any C call that can log.
	cpdump.SetLogger(base.Log())
	cpdump.SetDebugEBPF(cfg.DebugEBPF)

	retained, err := cleanupStale(base.Agent())
	for _, ring := range retained {
		base.Log().Warn("retained stale ring with published consumers",
			zap.String("ring", ring.Name), zap.Error(ring.Err),
		)
	}
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to clean up stale pdump state: %w", err),
			base.Close(),
		)
	}

	ringService := ring.NewRingService(base.Agent(), ring.WithLog(base.Log()))

	service := NewPdumpService(
		NewBackend(base.Agent(), WithBackendLog(base.Log())),
		newRingOwner(ringService),
		WithPdumpServiceLog(base.Log()),
	)
	base.AddService(pdumppb.PdumpService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		pdumppb.RegisterPdumpServiceServer(server, service)
	},
	)
	base.AddService(ring.ServiceName, func(server *grpc.Server) {
		ringService.Register(server)
	},
	)

	return &PdumpModule{
		Base:    base,
		service: service,
		log:     base.Log(),
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
