// Package pdump implements the control plane service for packet dumping.
// This file defines PdumpService, which handles gRPC requests for configuring
// and managing packet capture modules (identified by name).
// It keeps the published configuration of every name and serves the capture
// streams reading it.
package pdump

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

const moduleType = "pdump"

// Module is a published pdump module config with its capture rings.
type Module interface {
	// Rings returns the per-worker rings, valid until Free succeeds.
	Rings() []Ring
	// Free releases the module config and its rings, reporting
	// ffi.ErrStillReferenced while a live generation still holds it.
	Free() error
}

// Backend publishes and removes pdump module configs in shared memory.
type Backend interface {
	// UpdateModule builds a module config with fresh rings from the settings
	// and publishes it.
	//
	// On error nothing stays allocated.
	UpdateModule(name string, settings Settings) (Module, error)
	// DeleteModule removes the module config from the dataplane.
	DeleteModule(name string) error
}

// PdumpService provides packet capture functionality through a gRPC interface.
// It manages packet capture configurations and ring buffers.
type PdumpService struct {
	pdumppb.UnimplementedPdumpServiceServer

	backend Backend
	configs *configstore.Store[*capture]
	// life ends every stream once the service shuts down.
	life context.Context
	stop context.CancelFunc
	log  *zap.Logger
}

// PdumpServiceOption configures a packet capture service.
type PdumpServiceOption func(*pdumpServiceOptions)

type pdumpServiceOptions struct {
	Log *zap.Logger
}

func newPdumpServiceOptions() *pdumpServiceOptions {
	return &pdumpServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithPdumpServiceLog sets the logger for a packet capture service.
func WithPdumpServiceLog(log *zap.Logger) PdumpServiceOption {
	return func(o *pdumpServiceOptions) {
		o.Log = log
	}
}

// NewPdumpService initializes a new packet capture service.
func NewPdumpService(backend Backend, options ...PdumpServiceOption) *PdumpService {
	opts := newPdumpServiceOptions()
	for _, o := range options {
		o(opts)
	}

	life, stop := context.WithCancel(context.Background())

	return &PdumpService{
		backend: backend,
		configs: configstore.NewStore[*capture](),
		life:    life,
		stop:    stop,
		log:     opts.Log,
	}
}

// Shutdown ends every active ReadDump stream.
//
// Each handler returns only after the readers it started have stopped, so
// the shared memory may be released once the handlers have drained.
func (m *PdumpService) Shutdown() {
	m.stop()
}

// ListConfigs retrieves all configured packet capture modules.
func (m *PdumpService) ListConfigs(
	ctx context.Context,
	request *pdumppb.ListConfigsRequest,
) (*pdumppb.ListConfigsResponse, error) {
	return &pdumppb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

// ShowConfig retrieves the current configuration for a specific packet capture module.
func (m *PdumpService) ShowConfig(
	ctx context.Context,
	request *pdumppb.ShowConfigRequest,
) (*pdumppb.ShowConfigResponse, error) {
	name := request.GetName()

	current, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	return &pdumppb.ShowConfigResponse{Config: settingsProto(current.Settings())}, nil
}

// SetConfig updates or creates packet capture configuration.
// Supports partial updates via UpdateMask.
func (m *PdumpService) SetConfig(
	ctx context.Context,
	request *pdumppb.SetConfigRequest,
) (*pdumppb.SetConfigResponse, error) {
	name := request.GetName()

	err := m.configs.Update(name, func(current *capture, ok bool) (*capture, error) {
		settings := defaultSettings()
		if ok {
			settings = current.Settings()
		}
		settings = mergeSettings(settings, request)

		m.log.Debug("update config", zap.String("module", name))

		module, err := m.backend.UpdateModule(name, settings)
		if err != nil {
			return nil, err
		}

		return &capture{
			settings: settings,
			module:   module,
			readers:  newGate(),
			log:      m.log,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return &pdumppb.SetConfigResponse{}, nil
}

// DeleteConfig removes a packet capture configuration.
func (m *PdumpService) DeleteConfig(
	ctx context.Context,
	request *pdumppb.DeleteConfigRequest,
) (*pdumppb.DeleteConfigResponse, error) {
	name := request.GetName()

	err := m.configs.Delete(name, func(*capture) error {
		if err := m.backend.DeleteModule(name); err != nil {
			return status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
		}
		m.log.Info("deleted pdump config", zap.String("name", name))

		return nil
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		return nil, err
	}

	return &pdumppb.DeleteConfigResponse{}, nil
}

// ReadDump streams captured packets from the specified packet capture module.
//
// The stream continues until one of the following termination conditions occurs:
//   - The client disconnects (context cancellation from the gRPC stream)
//   - The service is shut down
//   - An error occurs while sending a packet record on the stream
//   - The configuration of this module is updated or deleted
//
// Every request reads the rings through its own read positions, so
// concurrent ReadDump requests do not interfere with each other. The handler
// returns only after its readers have stopped reading those rings.
func (m *PdumpService) ReadDump(req *pdumppb.ReadDumpRequest, stream grpc.ServerStreamingServer[pdumppb.Record]) error {
	name := req.GetName()

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	defer context.AfterFunc(m.life, cancel)()

	for {
		current, ok := m.configs.Get(name)
		if !ok {
			return status.Errorf(codes.NotFound, "config %q not found", name)
		}

		// A capture retired between the lookup and the start of the
		// stream is replaced by a newer one, or by nothing.
		if err := current.Read(ctx, stream.Send); !errors.Is(err, errRetired) {
			return err
		}
	}
}

// defaultSettings are the capture parameters of a config that was never
// updated: every packet on input, the system snapshot length and the
// smallest ring.
func defaultSettings() Settings {
	return Settings{
		Mode:     defaultMode,
		Snaplen:  defaultSnaplen,
		RingSize: uint32(minRingSize.Bytes()),
	}
}

// mergeSettings applies the fields the request names to the settings.
func mergeSettings(settings Settings, request *pdumppb.SetConfigRequest) Settings {
	if request.UpdateMask == nil {
		return settings
	}

	for _, path := range request.UpdateMask.Paths {
		switch path {
		case "filter":
			settings.Filter = request.Config.GetFilter()
		case "mode":
			mode := request.Config.GetMode()
			if mode == 0 {
				mode = defaultMode
			}
			settings.Mode = mode
		case "snaplen":
			settings.Snaplen = request.Config.GetSnaplen()
		case "ring_size":
			settings.RingSize = request.Config.GetRingSize()
		}
	}

	return settings
}

// settingsProto renders the settings as the configuration a client reads
// back.
func settingsProto(settings Settings) *pdumppb.Config {
	return &pdumppb.Config{
		Filter:   settings.Filter,
		Mode:     settings.Mode,
		Snaplen:  settings.Snaplen,
		RingSize: settings.RingSize,
	}
}
