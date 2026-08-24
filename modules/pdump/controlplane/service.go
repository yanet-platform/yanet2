// Package pdump implements the control plane service for packet dumping.
// This file defines PdumpService, which handles gRPC requests for configuring
// and managing packet capture modules (identified by name).
// It interacts with data plane agents via FFI (Foreign Function Interface)
// to apply capture settings (filters, mode, snaplen, ring buffer size)
// and to facilitate reading captured packets from shared ring buffers.
package pdump

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

const (
	errMsgConfigNameRequired = "module config name is required"
	moduleType               = "pdump"
)

// PdumpService provides packet capture functionality through a gRPC interface.
// It manages packet capture configurations and ring buffers.
type PdumpService struct {
	pdumppb.UnimplementedPdumpServiceServer

	mu         sync.RWMutex            // Protects concurrent access to configs and ringReaders.
	mutationMu sync.Mutex              // Serializes mutations and reader registration.
	agent      *ffi.Agent              // FFI agent used for data plane interaction.
	configs    map[string]*pdumpConfig // Map storing the active configuration for each pdump module, keyed by name.

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []*pdumpConfig
	// Slice of active ring buffer readers, each corresponding to an ongoing ReadDump stream.
	// Used to manage and terminate these readers during config updates or shutdown.
	ringReaders []ringReader
	quitCh      chan bool // Channel used to signal a graceful shutdown to all active ReadDump streams.
	log         *zap.Logger
}

// pdumpConfig stores the configuration for a pdump module,
// including packet filtering rules, capture mode, snapshot length, and ring buffer parameters.
type pdumpConfig struct {
	Filter    string        // libpcap expression string used to select packets for capture.
	DumpMode  uint32        // Bitmap that specifies the types of packets to capture (e.g., input, drops, ...).
	Snaplen   uint32        // Snapshot length, the maximum number of bytes to capture from each packet.
	Ring      *ringBuffer   // Configuration for the shared ring buffer, including per-worker size.
	FFIModule *ModuleConfig // FFI module configuration that needs to be freed when replaced
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *pdumpConfig) Free() error {
	if m.FFIModule == nil {
		return nil
	}
	return m.FFIModule.Free()
}

type ringReader struct {
	Name   string
	Ring   *ringBuffer
	Cancel context.CancelCauseFunc
	DoneCh chan bool
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
func NewPdumpService(agent *ffi.Agent, options ...PdumpServiceOption) *PdumpService {
	opts := newPdumpServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &PdumpService{
		agent:   agent,
		configs: map[string]*pdumpConfig{},
		quitCh:  make(chan bool),
		log:     opts.Log,
	}
}

// Shutdown signals a graceful shutdown to all active ReadDump streams.
func (m *PdumpService) Shutdown() {
	close(m.quitCh)
}

// ListConfigs retrieves all configured packet capture modules.
func (m *PdumpService) ListConfigs(
	ctx context.Context,
	request *pdumppb.ListConfigsRequest,
) (*pdumppb.ListConfigsResponse, error) {

	response := &pdumppb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

// ShowConfig retrieves the current configuration for a specific packet capture module.
func (m *PdumpService) ShowConfig(
	ctx context.Context,
	request *pdumppb.ShowConfigRequest,
) (*pdumppb.ShowConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, errMsgConfigNameRequired)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	return &pdumppb.ShowConfigResponse{
		Config: &pdumppb.Config{
			Filter:   config.Filter,
			Mode:     config.DumpMode,
			Snaplen:  config.Snaplen,
			RingSize: config.Ring.PerWorkerSize,
		},
	}, nil
}

// SetConfig updates or creates packet capture configuration.
// Supports partial updates via UpdateMask.
func (m *PdumpService) SetConfig(
	ctx context.Context,
	request *pdumppb.SetConfigRequest,
) (*pdumppb.SetConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, errMsgConfigNameRequired)
	}

	if request.Config == nil {
		return nil, fmt.Errorf("config is required")
	}

	m.mutationMu.Lock()
	defer m.mutationMu.Unlock()

	err := m.updateConfig(
		name,
		request,
		func(config *pdumpConfig) error {
			return m.updateModuleConfig(name, config)
		},
	)
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
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, errMsgConfigNameRequired)
	}

	m.mutationMu.Lock()
	defer m.mutationMu.Unlock()

	m.mu.RLock()
	config, ok := m.configs[name]
	m.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	err := m.withReaderDrain(
		name,
		fmt.Errorf("terminated by config deletion"),
		func() error {
			// Delete the module config from the data plane if it exists.
			if config.FFIModule != nil {
				if err := m.agent.DeleteModuleConfig(moduleType, name); err != nil {
					return status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
				}

				// The delete retired the generation holding this
				// config; retry the deferred ones, then retire this
				// one.
				m.reclaimDeferred()
				m.parkOrFree(config)
			}

			delete(m.configs, name)
			m.log.Info("deleted pdump config",
				zap.String("name", name),
			)
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	return &pdumppb.DeleteConfigResponse{}, nil
}

// transferConfigParameters transfers configuration parameters from the old config to the new FFI config.
// This includes dump mode, snaplen, filter, and ring buffer setup.
func (m *PdumpService) transferConfigParameters(
	name string,
	oldConfig *pdumpConfig,
	ffiConfig *ModuleConfig,
) error {
	m.log.Debug("set dump mode", zap.String("module", name))
	if err := ffiConfig.SetDumpMode(oldConfig.DumpMode); err != nil {
		return fmt.Errorf("failed to set dump mode for %s: %w", name, err)
	}

	m.log.Debug("set snaplen", zap.String("module", name))
	if err := ffiConfig.SetSnapLen(oldConfig.Snaplen); err != nil {
		return fmt.Errorf("failed to set snaplen for %s: %w", name, err)
	}

	m.log.Debug("set filter", zap.String("module", name))
	if err := ffiConfig.SetFilter(oldConfig.Filter); err != nil {
		return fmt.Errorf("failed to set pdump filter for %s: %w", name, err)
	}

	m.log.Debug("setup ring", zap.String("module", name))
	if err := ffiConfig.SetupRing(oldConfig.Ring); err != nil {
		return fmt.Errorf("failed to setup ring buffers for %s: %w", name, err)
	}

	return nil
}

// updateModuleConfig publishes the current configuration after all readers of
// the previous ring have stopped and the state lock has been reacquired.
func (m *PdumpService) updateModuleConfig(
	name string,
	modConfig *pdumpConfig,
) error {
	if m.agent == nil {
		return fmt.Errorf("pdump agent is required")
	}

	m.log.Debug("update config", zap.String("module", name))

	ffiConfig, err := NewModuleConfig(m.agent, name, WithModuleConfigLog(m.log))
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	if modConfig != nil {
		if err := m.transferConfigParameters(name, modConfig, ffiConfig); err != nil {
			if err := ffiConfig.Free(); err != nil {
				m.log.Error("failed to free unpublished pdump module",
					zap.String("name", name), zap.Error(err))
			}
			return err
		}
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{ffiConfig.AsFFIModule()}); err != nil {
		if err := ffiConfig.Free(); err != nil {
			m.log.Error("failed to free unpublished pdump module",
				zap.String("name", name), zap.Error(err))
		}
		return fmt.Errorf("failed to update module %s: %w", name, err)
	}

	// The update retired the generation holding the old module, so
	// retry this service's deferred handles, then retire the old module
	// itself: freed outright when dangling, parked while a pinned
	// generation still references it.
	m.reclaimDeferred()
	if modConfig != nil {
		m.parkOrFree(modConfig)
	}

	// Update the stored FFI module reference
	if modConfig != nil {
		modConfig.FFIModule = ffiConfig
	}

	return nil
}

// ReadDump streams captured packets from the specified packet capture module.
// This function establishes a continuous stream of packet data by:
//  1. Validating the target module (name)
//  2. Retrieving and cloning the ring buffer configuration for safe concurrent access
//  3. Spawning ring buffer readers that continuously monitor shared memory
//  4. Forwarding captured packet records to the gRPC stream
//
// The stream continues until one of the following termination conditions occurs:
//   - The client disconnects (context cancellation from the gRPC stream)
//   - The service is shut down (signaled via m.quitCh)
//   - An error occurs while sending a packet record on the stream
//   - The configuration of this module is updated (updateModuleConfig terminates matching readers)
//
// Note: Ring buffer readers operate on a cloned configuration to ensure thread safety
// and prevent interference between concurrent ReadDump requests.
func (m *PdumpService) ReadDump(req *pdumppb.ReadDumpRequest, stream grpc.ServerStreamingServer[pdumppb.Record]) error {
	ctx := stream.Context()

	name := req.GetName()
	if name == "" {
		return status.Error(codes.InvalidArgument, errMsgConfigNameRequired)
	}
	recordCh := make(chan *pdumppb.Record, 16)
	cancel, err := m.registerRingReaders(ctx, name, recordCh)
	if err != nil {
		return err
	}
	defer cancel(fmt.Errorf("streaming completed"))

	// Main streaming loop: forward packets from ring readers to gRPC client
	for {
		select {
		case rec, ok := <-recordCh:
			if !ok {
				// Ring readers have finished (likely due to context cancellation)
				m.log.Info("ring buffer readers have exited, terminating stream...")
				return nil
			}
			// Forward the packet record to the gRPC client
			if err := stream.Send(rec); err != nil {
				return err
			}
		case <-ctx.Done():
			// Client disconnected or request was cancelled
			return ctx.Err()
		case <-m.quitCh:
			// Service is shutting down gracefully
			m.log.Info("pdump service shut down; closing ReadDump request")
			return nil
		}
	}
}

// registerRingReaders validates the config and registers readers while
// preventing a concurrent config lifecycle operation from publishing a new
// ring buffer before registration completes.
func (m *PdumpService) registerRingReaders(
	ctx context.Context,
	name string,
	recordCh chan<- *pdumppb.Record,
) (context.CancelCauseFunc, error) {
	m.mutationMu.Lock()
	defer m.mutationMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, fmt.Errorf("config for %s does not exist", name)
	}
	if config.Ring.WorkerCount() == 0 {
		return nil, fmt.Errorf("config for %s is not initialized properly", name)
	}
	// Clone the ring buffer configuration to ensure thread safety.
	// This allows multiple concurrent ReadDump requests for the same module
	// without interfering with each other's read positions.
	ringCopy := config.Ring.Clone()

	return m.spawnRingReaders(ctx, name, ringCopy, recordCh), nil
}

func (m *PdumpService) updateConfig(
	name string,
	request *pdumppb.SetConfigRequest,
	publish func(config *pdumpConfig) error,
) error {
	newConfig, err := m.prepareConfig(name, request)
	if err != nil {
		return err
	}

	return m.withReaderDrain(
		name,
		fmt.Errorf("terminated by config update"),
		func() error {
			if err := publish(newConfig); err != nil {
				return err
			}
			m.configs[name] = newConfig

			return nil
		},
	)
}

func (m *PdumpService) prepareConfig(name string, request *pdumppb.SetConfigRequest) (*pdumpConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	newConfig := *defaultModuleConfig()
	config, ok := m.configs[name]
	if ok {
		// Create a copy of the config to ensure atomic updates.
		newRing := newConfig.Ring // Preserve the new ring.
		newConfig = *config
		newRing.PerWorkerSize = newConfig.Ring.PerWorkerSize
		newRing.ReadChunkSize = newConfig.Ring.ReadChunkSize
		newConfig.Ring = newRing // Restore the new ring.
	}

	if request.UpdateMask != nil && len(request.UpdateMask.Paths) > 0 {
		for _, path := range request.UpdateMask.Paths {
			switch path {
			case "filter":
				newConfig.Filter = request.Config.GetFilter()
			case "mode":
				mode := request.Config.GetMode()
				if mode > maxMode {
					return nil, fmt.Errorf("unknown pdump mode %b (max known %b)", mode, maxMode)
				}
				if mode == 0 {
					mode = defaultMode
				}
				newConfig.DumpMode = mode
			case "snaplen":
				snaplen := request.Config.GetSnaplen()
				if snaplen == 0 {
					m.log.Info("snaplen is zero, resetting to default value", zap.Uint32("default_snaplen", defaultSnaplen))
					snaplen = defaultSnaplen
				}
				newConfig.Snaplen = snaplen
			case "ring_size":
				size := request.Config.GetRingSize()
				if size < uint32(minRingSize.Bytes()) || size > maxRingSize {
					return nil, fmt.Errorf("ring size %s not in range [%s, %s]",
						datasize.ByteSize(size), minRingSize, datasize.ByteSize(maxRingSize))
				}
				newConfig.Ring.PerWorkerSize = size
			default:
				return nil, fmt.Errorf("unknown path '%s'", path)
			}
		}
	}
	return &newConfig, nil
}

func (m *PdumpService) stopRingReaders(name string, cause error) []chan bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	doneChannels := make([]chan bool, 0)
	for _, rr := range m.ringReaders {
		if rr.Name != name {
			continue
		}
		rr.Cancel(cause)
		doneChannels = append(doneChannels, rr.DoneCh)
	}
	m.ringReaders = slices.DeleteFunc(m.ringReaders, func(rr ringReader) bool {
		return rr.Name == name
	})
	return doneChannels
}

// withReaderDrain waits for readers before publishing state while the caller
// holds the lifecycle lock.
func (m *PdumpService) withReaderDrain(name string, cause error, publish func() error) error {
	doneChannels := m.stopRingReaders(name, cause)
	m.waitRingReaders(name, doneChannels)

	m.mu.Lock()
	defer m.mu.Unlock()
	return publish()
}

func (m *PdumpService) waitRingReaders(name string, doneChannels []chan bool) {
	for _, doneChannel := range doneChannels {
		m.log.Info("waiting for ring reader to complete", zap.String("name", name))
		<-doneChannel
	}
}

// spawnRingReaders initializes a new set of ring buffer readers for packet capture.
// It launches a goroutine that continuously reads packets and forwards them to the record channel.
// This function assumes m.mu is already locked by the caller.
func (m *PdumpService) spawnRingReaders(ctx context.Context, name string, ring *ringBuffer, recordCh chan<- *pdumppb.Record) context.CancelCauseFunc {
	ctx, cancel := context.WithCancelCause(ctx)
	reader := ringReader{
		Name:   name,
		Ring:   ring,
		Cancel: cancel,
		DoneCh: make(chan bool),
	}
	m.ringReaders = append(m.ringReaders, reader)

	m.log.Info("start ring readers", zap.Int("count", ring.WorkerCount()))
	go func() {
		info := ring.RunReaders(ctx, recordCh)
		m.log.Info("ring readers stopped", zap.Any("info", info))
		close(recordCh)
		close(reader.DoneCh)
	}()
	return cancel
}

// defaultModuleConfig creates a new module configuration with default values:
// - No packet filter (captures all packets)
// - Input packet capture mode
// - System default snapshot length
// - Minimum ring buffer size
func defaultModuleConfig() *pdumpConfig {
	return &pdumpConfig{
		Filter:   "",
		DumpMode: defaultMode,
		Snaplen:  defaultSnaplen,
		Ring: &ringBuffer{
			PerWorkerSize: uint32(minRingSize.Bytes()),
			ReadChunkSize: uint32(defaultReadChunkSize.Bytes()),
		},
	}
}

// parkOrFree frees the config when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *PdumpService) parkOrFree(handle *pdumpConfig) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred config, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *PdumpService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *PdumpService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
