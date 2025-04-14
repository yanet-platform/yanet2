package decap

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
	"github.com/yanet-platform/yanet2/controlplane/modules/decap/decappb"
)

type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey][]netip.Prefix
	log     *zap.SugaredLogger
}

func NewDecapService(agents []*ffi.Agent, log *zap.SugaredLogger) *DecapService {
	return &DecapService{
		agents:  agents,
		configs: map[instanceKey][]netip.Prefix{},
		log:     log,
	}
}

func (m *DecapService) ShowConfig(
	ctx context.Context,
	request *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name := request.GetTarget().GetModuleName()
	numa, err := m.getNUMAIndices(request.GetTarget().GetNuma())
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	configs := make([]*decappb.InstanceConfig, 0, len(numa))
	for _, numaIdx := range numa {
		key := instanceKey{name: name, numaIdx: numaIdx}
		if prefixes, ok := m.configs[key]; ok {
			instanceConfig := &decappb.InstanceConfig{
				Numa:     numaIdx,
				Prefixes: make([]string, 0, len(prefixes)),
			}

			for _, p := range prefixes {
				instanceConfig.Prefixes = append(instanceConfig.Prefixes, p.String())
			}
			configs = append(configs, instanceConfig)
		}
	}

	return &decappb.ShowConfigResponse{Configs: configs}, nil
}

func (m *DecapService) AddPrefixes(
	ctx context.Context,
	request *decappb.AddPrefixesRequest,
) (*decappb.AddPrefixesResponse, error) {

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
		prefixes, ok := m.configs[key]
		if !ok {
			m.configs[key] = toAdd
		} else {
			m.configs[key] = slices.Compact(
				slices.SortedFunc(
					slices.Values(slices.Concat(prefixes, toAdd)),
					func(a netip.Prefix, b netip.Prefix) int {
						return cmp.Compare(a.String(), b.String())
					}),
			)
		}
	}

	return &decappb.AddPrefixesResponse{}, m.updateModuleConfigs(name, numa)
}

func (m *DecapService) RemovePrefixes(
	ctx context.Context,
	request *decappb.RemovePrefixesRequest,
) (*decappb.RemovePrefixesResponse, error) {
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
		prefixes, ok := m.configs[key]
		if !ok {
			continue
		}

		m.configs[key] = slices.DeleteFunc(prefixes, func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		})

	}

	return &decappb.RemovePrefixesResponse{}, m.updateModuleConfigs(name, numa)
}

func (m *DecapService) updateModuleConfigs(
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
		for _, prefix := range m.configs[instanceKey{name: name, numaIdx: numaIdx}] {
			if err := config.PrefixAdd(prefix); err != nil {
				return fmt.Errorf("failed to add prefix for %s: %w", name, err)
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

func (m *DecapService) getNUMAIndices(requestedNuma []uint32) ([]uint32, error) {
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
