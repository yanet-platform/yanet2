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
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
)

const moduleType = "pdump"

// Module is a published pdump module config.
//
// It no longer exposes rings: a stream reads its capture's binding, which
// outlives any one module generation across a same-object update.
type Module interface {
	// Free releases the module config, reporting ffi.ErrStillReferenced
	// while a live generation still holds it.
	Free() error
}

// Backend publishes and removes pdump module configs in shared memory.
type Backend interface {
	// UpdateModule builds a module config from the settings, links its
	// ring by name and publishes it.
	//
	// On error nothing stays allocated.
	UpdateModule(name string, settings Settings) (Module, error)
	// DeleteModule removes the module config from the dataplane. It never
	// touches the ring the config was linked to.
	DeleteModule(name string) error
}

// PdumpService provides packet capture functionality through a gRPC interface.
// It manages packet capture configurations and the bindings their streams read.
type PdumpService struct {
	pdumppb.UnimplementedPdumpServiceServer

	backend   Backend
	ringOwner RingOwner
	configs   *configstore.Store[*capture]
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
//
// The ring owner resolves the ring names configs bind to; production
// passes the ring service this agent hosts, a test fakes it.
func NewPdumpService(backend Backend, ringOwner RingOwner, options ...PdumpServiceOption) *PdumpService {
	opts := newPdumpServiceOptions()
	for _, o := range options {
		o(opts)
	}

	life, stop := context.WithCancel(context.Background())

	return &PdumpService{
		backend:   backend,
		ringOwner: ringOwner,
		configs:   configstore.NewStore[*capture](),
		life:      life,
		stop:      stop,
		log:       opts.Log,
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
//
// Only the config fields the request carries change. ring_name is
// required to create a config, keeps the bound ring when absent on an
// update, and is invalid when carried empty. A newly bound ring must
// exist and be at least the pdump minimum capacity, whatever the snaplen;
// a same-object update keeps its binding without a new check. On any
// failure the published config, its binding and its streams are left
// exactly as they were.
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
		settings = mergeSettings(settings, request.GetConfig())

		if settings.RingName == "" {
			if !ok {
				return nil, status.Error(codes.InvalidArgument, "ring_name is required to create a config")
			}
			return nil, status.Error(codes.InvalidArgument, "ring_name must not be carried empty on an update; omit it to keep the bound ring")
		}

		handle, found := m.ringOwner.LookupHandle(settings.RingName)
		if !found {
			return nil, status.Errorf(codes.NotFound, "ring %q not found", settings.RingName)
		}

		// Comparing handles, not names, tells a same-object update from
		// a rebind: a ring deleted and recreated under the same name
		// gets a new handle.
		//
		// A same-object update retains the current binding instead of
		// taking a second lease, so an in-flight stream keeps reading it
		// with no reset.
		var b *binding
		if ok && current.Binding().Handle() == handle {
			b = current.Binding()
			b.Retain()
		} else {
			lease, err := m.ringOwner.Acquire(settings.RingName, handle)
			if err != nil {
				// A handle gone between the lookup above and this
				// acquire is the same "ring is gone" case the lookup
				// itself would have reported as NotFound.
				//
				// Any other acquire failure is the owner's own
				// problem, not the caller's request.
				code := codes.Internal
				if errors.Is(err, ring.ErrHandleGone) {
					code = codes.NotFound
				}
				return nil, status.Errorf(code, "ring %q: %v", settings.RingName, err)
			}
			// A ring below the minimum could refuse a large record, and
			// the capture would silently miss that packet.
			if capacity := lease.Capacity(); capacity < MinRingCapacity {
				lease.Release()
				return nil, status.Errorf(
					codes.InvalidArgument,
					"ring %q capacity %d is below the pdump minimum %d",
					settings.RingName, capacity, MinRingCapacity,
				)
			}
			b = newBinding(settings.RingName, lease, withBindingLog(m.log))
		}

		m.log.Debug("update config", zap.String("module", name))
		module, err := m.backend.UpdateModule(name, settings)
		if err != nil {
			b.Release()
			return nil, status.Errorf(codes.Internal, "failed to update module config %q: %v", name, err)
		}

		return &capture{
			settings: settings,
			module:   module,
			binding:  b,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return &pdumppb.SetConfigResponse{}, nil
}

// DeleteConfig removes a packet capture configuration.
//
// The ring the config was linked to is never deleted.
func (m *PdumpService) DeleteConfig(
	ctx context.Context,
	request *pdumppb.DeleteConfigRequest,
) (*pdumppb.DeleteConfigResponse, error) {
	name := request.GetName()

	err := m.configs.Delete(name, func(*capture) error {
		if err := m.backend.DeleteModule(name); err != nil {
			code := codes.Internal
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				// A chain still references the config.
				code = codes.FailedPrecondition
			}
			return status.Errorf(code, "failed to delete module config %q: %v", name, err)
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
//   - The configuration of this module is deleted or rebound to another ring
//
// Every request reads its binding's ring through its own read positions, so
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

		switch err := current.Read(ctx, stream.Send); {
		case errors.Is(err, errRetired):
			// A capture retired between the lookup and the start of
			// the stream is replaced by a newer one, or by nothing.
		case err != nil:
			return err
		default:
			// A client that abandoned the stream gets its own error,
			// while a shutdown, a delete or a rebind ends it without
			// one.
			return stream.Context().Err()
		}
	}
}

// defaultSettings are the capture parameters of a config that was never
// updated: every packet on input and the system snapshot length.
//
// The ring name stays unset: a config must carry one to be created.
func defaultSettings() Settings {
	return Settings{
		Mode:    defaultMode,
		Snaplen: defaultSnaplen,
	}
}

// mergeSettings applies the fields the config carries to the settings.
func mergeSettings(settings Settings, config *pdumppb.Config) Settings {
	if config == nil {
		return settings
	}

	if config.Filter != nil {
		settings.Filter = config.GetFilter()
	}
	if config.Mode != nil {
		mode := config.GetMode()
		if mode == 0 {
			mode = defaultMode
		}
		settings.Mode = mode
	}
	if config.Snaplen != nil {
		settings.Snaplen = config.GetSnaplen()
	}
	if config.RingName != nil {
		// An explicitly empty ring name is carried through as-is, so
		// the config update's required-ring-name check reports it
		// rather than silently keeping the old binding.
		settings.RingName = config.GetRingName()
	}

	return settings
}

// settingsProto renders the settings as the configuration a client reads
// back.
func settingsProto(settings Settings) *pdumppb.Config {
	return &pdumppb.Config{
		Filter:   proto.String(settings.Filter),
		Mode:     proto.Uint32(settings.Mode),
		Snaplen:  proto.Uint32(settings.Snaplen),
		RingName: proto.String(settings.RingName),
	}
}
