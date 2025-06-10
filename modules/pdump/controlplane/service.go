// Package pdump implements the control plane service for packet dumping.
// This file defines PdumpService, which handles gRPC requests for configuring
// and managing packet capture instances (identified by name and NUMA node).
// It interacts with data plane agents via FFI (Foreign Function Interface)
// to apply capture settings (filters, mode, snaplen, ring buffer size)
// and to facilitate reading captured packets from shared ring buffers.
package pdump

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

// PdumpService provides packet capture functionality through a gRPC interface.
// It manages packet capture configurations and ring buffers across multiple NUMA nodes.
type PdumpService struct {
	pdumppb.UnimplementedPdumpServiceServer

	mu      sync.Mutex                      // Protects concurrent access to agents, configs, and ringReaders.
	agents  []*ffi.Agent                    // Slice of FFI agents, typically one per NUMA node, used for data plane interaction.
	configs map[instanceKey]*instanceConfig // Map storing the active configuration for each packet capture instance, keyed by instanceKey.
	// Slice of active ringBuffer instances, each corresponding to an ongoing ReadDump stream.
	// Used to manage and terminate these readers during config updates or shutdown.
	ringReaders []ringReader
	quitCh      chan bool // Channel used to signal a graceful shutdown to all active ReadDump streams.
	log         *zap.SugaredLogger
}

// instanceConfig stores the configuration for a packet capture instance,
// including packet filtering rules, capture mode, snapshot length, and ring buffer parameters.
type instanceConfig struct {
	filter   string           // libpcap expression string used to select packets for capture.
	dumpMode pdumppb.DumpMode // Specifies the queue of packets to capture (e.g., input, drops, or both).
	snaplen  uint32           // Snapshot length, the maximum number of bytes to capture from each packet.
	ring     *ringBuffer      // Configuration for the shared ring buffer used by this instance, including per-worker size.
}

type ringReader struct {
	key    instanceKey
	ring   *ringBuffer
	cancel context.CancelCauseFunc
	doneCh chan bool
}

// NewPdumpService initializes a new packet capture service with the specified agents and logger.
func NewPdumpService(agents []*ffi.Agent, log *zap.SugaredLogger) *PdumpService {
	return &PdumpService{
		agents:  agents,
		configs: map[instanceKey]*instanceConfig{},
		quitCh:  make(chan bool),
		log:     log,
	}
}

// ListConfigs retrieves all configured packet capture instances across NUMA nodes.
func (m *PdumpService) ListConfigs(
	ctx context.Context,
	request *pdumppb.ListConfigsRequest,
) (*pdumppb.ListConfigsResponse, error) {

	response := &pdumppb.ListConfigsResponse{
		NumaConfigs: make([]*pdumppb.NumaConfigs, len(m.agents)),
	}
	for idx := range m.agents {
		response.NumaConfigs[idx] = &pdumppb.NumaConfigs{
			Numa: uint32(idx),
		}
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range maps.Keys(m.configs) {
		numaConfigs := response.NumaConfigs[key.numaIdx]
		numaConfigs.Configs = append(numaConfigs.Configs, key.name)
	}

	return response, nil
}

// ShowConfig retrieves the current configuration for a specific packet capture instance.
func (m *PdumpService) ShowConfig(
	ctx context.Context,
	request *pdumppb.ShowConfigRequest,
) (*pdumppb.ShowConfigResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	key := instanceKey{name: name, numaIdx: numa}
	response := &pdumppb.ShowConfigResponse{Numa: numa}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.configs[key]; ok {
		response.Config = &pdumppb.Config{
			Filter:   config.filter,
			Mode:     config.dumpMode,
			Snaplen:  config.snaplen,
			RingSize: config.ring.perWorkerSize,
		}

	}

	return response, nil
}

// SetFilter configures the packet filter for a specific instance using libpcap syntax.
func (m *PdumpService) SetFilter(
	ctx context.Context,
	request *pdumppb.SetFilterRequest,
) (*pdumppb.SetFilterResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	filter := request.GetFilter()

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	config, ok := m.configs[key]
	if !ok {
		config = defaultModuleConfig()
		m.configs[key] = config
	}
	config.filter = filter

	return &pdumppb.SetFilterResponse{}, m.updateModuleConfig(name, numa)
}

// SetDumpMode sets the packet capture mode for a specific instance,
// determining whether to capture incoming, dropped, or both types of packets.
func (m *PdumpService) SetDumpMode(
	ctx context.Context,
	request *pdumppb.SetDumpModeRequest,
) (*pdumppb.SetDumpModeResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	mode := request.GetMode()

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	config, ok := m.configs[key]
	if !ok {
		config = defaultModuleConfig()
		m.configs[key] = config
	}
	config.dumpMode = mode

	return &pdumppb.SetDumpModeResponse{}, m.updateModuleConfig(name, numa)
}

// SetSnapLen sets the maximum number of bytes to capture per packet.
// If the provided snaplen is zero, it defaults to the system's default value.
func (m *PdumpService) SetSnapLen(
	ctx context.Context,
	request *pdumppb.SetSnapLenRequest,
) (*pdumppb.SetSnapLenResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	snaplen := request.GetSnaplen()
	if snaplen == 0 {
		m.log.Infof("snaplen is zero, resetting to default value %d", defaultSnaplen)
		snaplen = defaultSnaplen
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	config, ok := m.configs[key]
	if !ok {
		config = defaultModuleConfig()
		m.configs[key] = config
	}
	config.snaplen = snaplen

	return &pdumppb.SetSnapLenResponse{}, m.updateModuleConfig(name, numa)
}

// SetWorkerRingSize configures the ring buffer size for each worker.
// The size must fall within the range [minRingSize, maxRingSize].
func (m *PdumpService) SetWorkerRingSize(
	ctx context.Context,
	request *pdumppb.SetWorkerRingSizeRequest,
) (*pdumppb.SetWorkerRingSizeResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	size := request.GetRingSize()
	if size < uint32(minRingSize.Bytes()) || size > maxRingSize {
		return nil, fmt.Errorf("ring size %s not in range [%s, %s]",
			datasize.ByteSize(size), minRingSize, datasize.ByteSize(maxRingSize))
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	config, ok := m.configs[key]
	if !ok {
		config = defaultModuleConfig()
		m.configs[key] = config
	}
	config.ring.perWorkerSize = size
	config.ring.workers = nil

	return &pdumppb.SetWorkerRingSizeResponse{}, m.updateModuleConfig(name, numa)
}

// updateModuleConfig applies the current configuration to the specified module instance.
// This function assumes m.mu is already locked by the caller.
// The process involves:
//  1. Terminating all active ring readers for any instance to prevent memory access
//     violations, as reconfiguring a module can deallocate shared ring buffers.
//  2. Creating a new FFI module configuration object for the specified instance.
//  3. Applying the stored settings (capture mode, snaplen, filter, ring buffer parameters)
//     from m.configs to the FFI configuration. If no specific configuration exists,
//     the module will be updated with default settings from the new FFI config.
//  4. Updating the data plane module via the FFI interface with the new configuration.
func (m *PdumpService) updateModuleConfig(
	name string,
	numa uint32,
) error {
	// m.mu is held by the caller. First, terminate all active ring readers.
	// This is crucial because changing the module configuration (via ffiConfig.AsFFIModule()
	// and agent.UpdateModules) can lead to deallocating the shared memory
	// used by the ring buffers. If readers were still active during or after this
	// deallocation, they would attempt to access invalid memory, leading to segmentation faults.

	// Terminate only the ring readers associated with the specific 'name' and 'numa'
	// being updated.
	key := instanceKey{name: name, numaIdx: numa}

	// First pass: terminate matching ring readers and wait for completion
	for _, rr := range m.ringReaders {
		if rr.key == key {
			rr.cancel(fmt.Errorf("terminated by config update"))
			m.log.Infof("waiting for ring reader %v to complete", key)
			<-rr.doneCh
		}
	}

	// Second pass: remove terminated ring readers from the slice
	// We use a write index to compact the slice in-place, avoiding allocations
	writeIdx := 0
	for readIdx := range m.ringReaders {
		if m.ringReaders[readIdx].key != key {
			if writeIdx != readIdx {
				// Keep this ring reader - copy it to the write position
				m.ringReaders[writeIdx] = m.ringReaders[readIdx]
			}
			writeIdx++
		}
		// Skip terminated ring readers (don't increment writeIdx)
	}
	// Truncate the slice to remove the terminated entries
	m.ringReaders = m.ringReaders[:writeIdx]

	m.log.Debugw("update config", zap.String("module", name), zap.Uint32("numa", numa))

	agent := m.agents[numa]

	ffiConfig, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	instanceConfig := m.configs[key]
	if instanceConfig != nil {
		m.log.Debugw("set dump mode", zap.String("module", name), zap.Uint32("numa", numa))
		if err := ffiConfig.SetDumpMode(instanceConfig.dumpMode); err != nil {
			return fmt.Errorf("failed to set dump mode for %s: %w", name, err)
		}
		m.log.Debugw("set snaplen", zap.String("module", name), zap.Uint32("numa", numa))
		if err := ffiConfig.SetSnapLen(instanceConfig.snaplen); err != nil {
			return fmt.Errorf("failed to set snaplen for %s: %w", name, err)
		}
		m.log.Debugw("set filter", zap.String("module", name), zap.Uint32("numa", numa))
		if err := ffiConfig.SetFilter(instanceConfig.filter); err != nil {
			return fmt.Errorf("failed to set pdump filter for %s: %w", name, err)
		}
		m.log.Debugw("setup ring", zap.String("module", name), zap.Uint32("numa", numa))
		if err := ffiConfig.SetupRing(instanceConfig.ring, m.log); err != nil {
			return fmt.Errorf("failed to setup ring buffers for %s: %w", name, err)
		}
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{ffiConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module %s: %w", name, err)
	}

	m.log.Infow("successfully updated module",
		zap.String("name", name),
		zap.Uint32("numa", numa),
	)
	return nil
}

// ReadDump streams captured packets from the specified packet capture instance.
// This function establishes a continuous stream of packet data by:
//  1. Validating the target instance (name and NUMA node)
//  2. Retrieving and cloning the ring buffer configuration for safe concurrent access
//  3. Spawning ring buffer readers that continuously monitor shared memory
//  4. Forwarding captured packet records to the gRPC stream
//
// The stream continues until one of the following termination conditions occurs:
//   - The client disconnects (context cancellation from the gRPC stream)
//   - The service is shut down (signaled via m.quitCh)
//   - An error occurs while sending a packet record on the stream
//   - The configuration of this specific packet capture instance is updated
//     (updateModuleConfig selectively terminates only matching readers)
//
// Note: Ring buffer readers operate on a cloned configuration to ensure thread safety
// and prevent interference between concurrent ReadDump requests for the same instance.
func (m *PdumpService) ReadDump(req *pdumppb.ReadDumpRequest, stream grpc.ServerStreamingServer[pdumppb.Record]) error {
	ctx := stream.Context()

	name, numa, err := req.Target.Validate(uint32(len(m.agents)))
	if err != nil {
		return fmt.Errorf("validate ReadDump target: %w", err)
	}
	key := instanceKey{name: name, numaIdx: numa}
	m.mu.Lock()
	config, ok := m.configs[key]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("config for %s on NUMA node %d does not exist", name, numa)
	}
	if len(config.ring.workers) == 0 {
		m.mu.Unlock()
		return fmt.Errorf("config for %s on NUMA node %d is not initialized properly", name, numa)
	}
	// Clone the ring buffer configuration to ensure thread safety.
	// This allows multiple concurrent ReadDump requests for the same instance
	// without interfering with each other's read positions.
	ringCopy := config.ring.Clone()

	// Create a buffered channel for packet records to decouple ring reading from stream sending
	recordCh := make(chan *pdumppb.Record, 16)
	cancel := m.spawnRingReaders(ctx, key, ringCopy, recordCh)
	defer cancel(fmt.Errorf("streaming completed"))

	// We can unlock only after spawning ring readers, as appending ringReader requires
	// a reader pointing to valid shared memory.
	m.mu.Unlock()

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

// spawnRingReaders initializes a new set of ring buffer readers for packet capture.
// It launches a goroutine that continuously reads packets and forwards them to the record channel.
// This function assumes m.mu is already locked by the caller.
func (m *PdumpService) spawnRingReaders(ctx context.Context, key instanceKey, ring *ringBuffer, recordCh chan<- *pdumppb.Record) context.CancelCauseFunc {
	ctx, cancel := context.WithCancelCause(ctx)
	reader := ringReader{
		key:    key,
		ring:   ring,
		cancel: cancel,
		doneCh: make(chan bool),
	}
	m.ringReaders = append(m.ringReaders, reader)

	m.log.Infof("start %d ring readers", len(ring.workers))
	go func() {
		info := ring.runReaders(ctx, recordCh)
		m.log.Infof("ring readers stopped due to %v", info)
		close(recordCh)
		close(reader.doneCh)
	}()
	return cancel
}

// defaultModuleConfig creates a new instance configuration with default values:
// - No packet filter (captures all packets)
// - Input packet capture mode
// - System default snapshot length
// - Minimum ring buffer size
func defaultModuleConfig() *instanceConfig {
	return &instanceConfig{
		filter:   "",
		dumpMode: pdumppb.DumpMode_PDUMP_DUMP_INPUT,
		snaplen:  defaultSnaplen,
		ring: &ringBuffer{
			perWorkerSize: uint32(minRingSize.Bytes()),
			readChunkSize: uint32(defaultReadChunkSize.Bytes()),
		},
	}
}
