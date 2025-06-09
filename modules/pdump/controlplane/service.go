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

type PdumpService struct {
	pdumppb.UnimplementedPdumpServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey]*instanceConfig
	quitCh  chan bool
	log     *zap.SugaredLogger
}

type instanceConfig struct {
	filter   string
	dumpMode pdumppb.DumpMode
	snaplen  uint32
	ring     *ringBuffer
}

func NewPdumpService(agents []*ffi.Agent, log *zap.SugaredLogger) *PdumpService {
	return &PdumpService{
		agents:  agents,
		configs: map[instanceKey]*instanceConfig{},
		quitCh:  make(chan bool),
		log:     log,
	}
}

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
	for key := range maps.Keys(m.configs) {
		numaConfigs := response.NumaConfigs[key.numaIdx]
		numaConfigs.Configs = append(numaConfigs.Configs, key.name)
	}

	return response, nil
}

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
		m.log.Infof("snaplen is zero, resetting to defaultSnaplen %d", snaplen, defaultSnaplen)
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

func (m *PdumpService) updateModuleConfig(
	name string,
	numa uint32,
) error {
	m.log.Debugw("update config", zap.String("module", name), zap.Uint32("numa", numa))

	agent := m.agents[numa]

	ffiConfig, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	instanceConfig := m.configs[instanceKey{name: name, numaIdx: numa}]
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

func (m *PdumpService) ReadDump(req *pdumppb.ReadDumpRequest, stream grpc.ServerStreamingServer[pdumppb.Record]) error {
	name, numa, err := req.Target.Validate(uint32(len(m.agents)))
	if err != nil {
		return fmt.Errorf("validate ReadDump target: %w", err)
	}
	key := instanceKey{name: name, numaIdx: numa}
	config, ok := m.configs[key]
	if !ok {
		return fmt.Errorf("config for %s on NUMA node %d does not exist", name, numa)
	}
	if len(config.ring.workers) == 0 {
		return fmt.Errorf("config for %s on NUMA node %d is not initialized properly", name, numa)
	}

	recordCh := make(chan *pdumppb.Record, 16)
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	go m.runRingReaders(ctx, config, recordCh)

	for {
		select {
		case rec, ok := <-recordCh:
			if !ok {
				return nil
			}
			if err := stream.Send(rec); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-m.quitCh:
			m.log.Info("pdump service shut down; closing ReadDump request")
			return nil
		}
	}
}

func (m *PdumpService) runRingReaders(ctx context.Context, config *instanceConfig, recordCh chan<- *pdumppb.Record) {
	m.log.Infof("start %d ring readers", len(config.ring.workers))
	info := config.ring.runReaders(ctx, recordCh)
	m.log.Infof("ring readers stopped due to %v", info)
	close(recordCh)
}

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
