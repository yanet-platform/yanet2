package dscp

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb"
)

type DscpService struct {
	dscppb.UnimplementedDscpServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey]*moduleConfig
	log     *zap.SugaredLogger
}

type moduleConfig struct {
	prefixes []netip.Prefix
	dscpCfg  dscpConfig
}

type dscpConfig struct {
	flag uint8
	mark uint8
}

func NewDscpService(agents []*ffi.Agent, log *zap.SugaredLogger) *DscpService {
	return &DscpService{
		agents:  agents,
		configs: map[instanceKey]*moduleConfig{},
		log:     log,
	}
}

func (m *DscpService) ShowConfig(
	ctx context.Context,
	request *dscppb.ShowConfigRequest,
) (*dscppb.ShowConfigResponse, error) {
	name := request.GetTarget().GetModuleName()
	numa, err := m.getNUMAIndices(request.GetTarget().GetNuma())
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	configs := make([]*dscppb.InstanceConfig, 0, len(numa))
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		if config, ok := m.configs[key]; ok {
			instanceConfig := &dscppb.InstanceConfig{
				Numa:     numaIdx,
				Prefixes: make([]string, 0, len(config.prefixes)),
				DscpConfig: &dscppb.DscpConfig{
					Flag: uint32(config.dscpCfg.flag),
					Mark: uint32(config.dscpCfg.mark),
				},
			}

			for _, p := range config.prefixes {
				instanceConfig.Prefixes = append(instanceConfig.Prefixes, p.String())
			}
			configs = append(configs, instanceConfig)
		}
	}

	return &dscppb.ShowConfigResponse{Configs: configs}, nil
}

func (m *DscpService) AddPrefixes(
	ctx context.Context,
	request *dscppb.AddPrefixesRequest,
) (*dscppb.AddPrefixesResponse, error) {

	name := request.GetTarget().GetModuleName()
	numa, err := m.getNUMAIndices(request.GetTarget().GetNuma())
	if err != nil {
		return nil, err
	}

	toAdd := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, prefixStr := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(prefixStr)
		if err != nil {
			return nil, err
		}
		toAdd = append(toAdd, prefix.Masked())
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config, ok := m.configs[key]
		if !ok {
			m.configs[key] = &moduleConfig{
				prefixes: toAdd,
				dscpCfg:  dscpConfig{flag: 0, mark: 0},
			}
		} else {
			config.prefixes = slices.Compact(
				slices.SortedFunc(
					slices.Values(slices.Concat(config.prefixes, toAdd)),
					func(a netip.Prefix, b netip.Prefix) int {
						return cmp.Compare(a.String(), b.String())
					}),
			)
		}
	}

	return &dscppb.AddPrefixesResponse{}, m.updateModuleConfigs(name, numa)
}

func (m *DscpService) RemovePrefixes(
	ctx context.Context,
	request *dscppb.RemovePrefixesRequest,
) (*dscppb.RemovePrefixesResponse, error) {
	name := request.GetTarget().GetModuleName()
	numa, err := m.getNUMAIndices(request.GetTarget().GetNuma())
	if err != nil {
		return nil, err
	}

	toRemove := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, prefixStr := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(prefixStr)
		if err != nil {
			return nil, err
		}
		toRemove = append(toRemove, prefix.Masked())
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config, ok := m.configs[key]
		if !ok {
			continue
		}

		config.prefixes = slices.DeleteFunc(config.prefixes, func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		})
	}

	return &dscppb.RemovePrefixesResponse{}, m.updateModuleConfigs(name, numa)
}

func (m *DscpService) SetDscpMarking(
	ctx context.Context,
	request *dscppb.SetDscpMarkingRequest,
) (*dscppb.SetDscpMarkingResponse, error) {
	name := request.GetTarget().GetModuleName()
	numa, err := m.getNUMAIndices(request.GetTarget().GetNuma())
	if err != nil {
		return nil, err
	}

	dscpCfg := request.GetDscpConfig()
	if dscpCfg == nil {
		return nil, status.Error(codes.InvalidArgument, "DSCP config is required")
	}

	// Validate flag value
	flag := uint8(dscpCfg.GetFlag())
	if flag > 2 {
		return nil, status.Error(codes.InvalidArgument, "invalid flag value (must be 0, 1, or 2)")
	}

	// Validate mark value (6-bit field)
	mark := uint8(dscpCfg.GetMark())
	if mark > 63 {
		return nil, status.Error(codes.InvalidArgument, "invalid mark value (must be 0-63)")
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		config, ok := m.configs[key]
		if !ok {
			m.configs[key] = &moduleConfig{
				prefixes: []netip.Prefix{},
				dscpCfg: dscpConfig{
					flag: flag,
					mark: mark,
				},
			}
		} else {
			config.dscpCfg.flag = flag
			config.dscpCfg.mark = mark
		}
	}

	return &dscppb.SetDscpMarkingResponse{}, m.updateModuleConfigs(name, numa)
}

func (m *DscpService) updateModuleConfigs(
	name string,
	numaIndices []uint32,
) error {
	m.log.Debugw("update config", zap.String("module", name), zap.Uint32s("numa", numaIndices))

	configs := make([]*ModuleConfig, 0, len(numaIndices))
	for _, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]

		config, err := NewModuleConfig(agent, name)
		if err != nil {
			return fmt.Errorf("failed to create %q module config: %w", name, err)
		}

		moduleConfig := m.configs[instanceKey{name: name, numaIdx: numaIdx}]
		if moduleConfig != nil {
			// Add prefixes
			for _, prefix := range moduleConfig.prefixes {
				if err := config.PrefixAdd(prefix); err != nil {
					return fmt.Errorf("failed to add prefix for %s: %w", name, err)
				}
			}

			// Set DSCP marking
			if err := config.SetDscpMarking(moduleConfig.dscpCfg.flag, moduleConfig.dscpCfg.mark); err != nil {
				return fmt.Errorf("failed to set DSCP marking for %s: %w", name, err)
			}
		}

		configs = append(configs, config)
	}

	for _, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]
		config := configs[numaIdx]

		if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
			return fmt.Errorf("failed to update module: %w", err)
		}

		m.log.Infow("successfully updated module",
			zap.String("name", name),
			zap.Uint32("numa", numaIdx),
		)
	}
	return nil
}

func (m *DscpService) getNUMAIndices(requestedNuma []uint32) ([]uint32, error) {
	numaIndices := slices.Compact(slices.Sorted(slices.Values(requestedNuma)))

	slices.Sort(requestedNuma)
	if !slices.Equal(numaIndices, requestedNuma) {
		return nil, status.Error(codes.InvalidArgument, "duplicate NUMA indices in the request")
	}
	if len(numaIndices) > 0 && int(numaIndices[len(numaIndices)-1]) >= len(m.agents) {
		return nil, status.Error(codes.InvalidArgument, "NUMA indices are out of range")
	}
	if len(numaIndices) == 0 {
		for idx := range m.agents {
			numaIndices = append(numaIndices, uint32(idx))
		}
	}
	return numaIndices, nil
}
